package rc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsupload"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

// ErrCheckpointSave remains distinguishable across the isolated worker protocol.
var ErrCheckpointSave = errors.New("transfer resume checkpoint could not be saved")

// Worker processes isolate legacy global account clients and stdout, and can be
// cancelled without allowing a partially completed transfer to trigger deletion.
func worker(ctx context.Context, kind string, uid uint64, src, dst string, rate int64) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
	if settings.Threads < 1 {
		settings.Threads = 1
	}
	encoded, _ := json.Marshal(settings)
	cmd := exec.CommandContext(ctx, exe, "__rc_transfer", kind, strconv.FormatUint(uid, 10), src, dst, strconv.FormatInt(rate, 10), string(encoded))
	return executeWorkerCommand(ctx, cmd, kind, uid)
}

func executeWorkerCommand(ctx context.Context, cmd *exec.Cmd, kind string, uid uint64) error {
	output := &workerOutput{ctx: ctx}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	var cancelOnce sync.Once
	cmd.Cancel = func() error {
		cancelOnce.Do(func() { _, _ = fmt.Fprintln(input, "cancel") })
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	var failureStop *time.Timer // Published before the output-copy goroutine exits.
	var stopOnce sync.Once
	output.stop = func() {
		stopOnce.Do(func() {
			// Local output failure is not user cancellation. Allow checkpoint
			// cleanup before bounding an unresponsive child.
			failureStop = time.AfterFunc(cmd.WaitDelay, func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			})
			_ = cmd.Cancel()
		})
	}
	err = cmd.Run()
	output.finish()
	if failureStop != nil {
		failureStop.Stop()
	}
	if output.callbackErr != nil || output.protocolErr != nil {
		var checkpointErr error
		if output.checkpointFailed {
			checkpointErr = ErrCheckpointSave
		}
		return errors.Join(ctx.Err(), output.callbackErr, output.protocolErr, checkpointErr)
	}
	if output.checkpointFailed {
		return errors.Join(ctx.Err(), ErrCheckpointSave)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if output.reset {
			return errResumeIdentity
		}
		return fmt.Errorf("%s transfer failed (account %d); source retained: %w", kind, uid, err)
	}
	return nil
}

// RunWorker receives paths and an account ID only, never credential arguments.
func RunWorker(args []string) (err error) {
	if len(args) != 5 && len(args) != 6 {
		return errors.New("invalid worker arguments")
	}
	kind := args[0]
	uid, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return err
	}
	rate, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil || rate < 0 {
		return errors.New("invalid transfer rate")
	}
	settings := workerSettings{Threads: 4}
	if len(args) == 6 {
		if err := json.Unmarshal([]byte(args[5]), &settings); err != nil {
			return errors.New("invalid worker settings")
		}
	}
	if settings.Threads < 1 || settings.Threads > 32 {
		return errors.New("invalid download threads")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if scanner.Text() == "cancel" {
				cancel()
				return
			}
		}
		cancel()
	}()
	progressOut := os.Stdout
	quiet, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	os.Stdout = quiet
	defer func() { os.Stdout = progressOut; quiet.Close() }()
	reporter := newWorkerReporter(progressOut, cancel)
	defer func() {
		if reportErr := reporter.Err(); reportErr != nil {
			err = errors.Join(err, fmt.Errorf("worker progress report: %w", reportErr))
		}
	}()
	report := reporter.Report
	onProgress := func(done, total, rate int64) { report(transferProgress{Done: done, Total: total, Rate: rate}) }
	user, err := pcsconfig.Config.SwitchUser(&pcsconfig.BaiduBase{UID: uid})
	if err != nil {
		return err
	}
	pcs := user.BaiduPCS()
	pcsconfig.Config.MaxUploadRate = rate
	pcsconfig.Config.MaxDownloadRate = rate
	executor := &taskframework.TaskExecutor{IsFailedDeque: true}
	executor.SetParallel(1)
	if kind == "upload" {
		info, err := os.Stat(args[2])
		if err != nil {
			return err
		}
		if info.Size() == 0 {
			return uploadEmptyFile(ctx, pcs, args[3], pcsconfig.Config.PCSHTTPClient())
		}
		// Each target has a separate resume database, preventing workers from
		// overwriting each other's state or resuming against another account/path.
		key := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", uid, args[2], args[3])))
		stateDir := filepath.Join(pcsconfig.GetConfigDir(), "rc-state")
		if err = os.MkdirAll(stateDir, 0700); err != nil {
			return err
		}
		db, err := pcsupload.NewUploadingDatabaseAt(filepath.Join(stateDir, fmt.Sprintf("%x.json", key)))
		if err != nil {
			return err
		}
		defer db.Close()
		statistic := &pcsupload.UploadStatistic{}
		statistic.StartTimer()
		executor.Append(&pcsupload.UploadTaskUnit{Context: ctx, Progress: onProgress, OnTransferError: func(err error) {
			if errors.Is(err, pcsupload.ErrCheckpointSave) {
				report(transferProgress{CheckpointFailed: true})
			}
		}, IgnoreCachedMatch: true, LocalFileChecksum: checksum.NewLocalFileChecksum(args[2], int(baidupcs.SliceMD5Size)), SavePath: args[3], PCS: pcs, UploadingDatabase: db, Parallel: 1, PrintFormat: pcsupload.DefaultPrintFormat, UploadStatistic: statistic, Policy: baidupcs.OverWritePolicy}, 0)
	} else if kind == "download" || kind == "probe" {
		info, e := workerDownloadInfo(ctx, pcs, args[2])
		if e != nil {
			return e
		}
		if kind == "download" {
			if err = os.MkdirAll(filepath.Dir(args[3]), 0755); err != nil {
				return err
			}
		}
		if info.Size == 0 && kind == "probe" {
			report(transferProgress{Total: 0})
			return nil
		}
		statistic := &pcsdownload.DownloadStatistic{}
		statistic.StartTimer()
		cfg := &downloader.Config{Mode: transfer.RangeGenMode_BlockSize, MaxParallel: settings.Threads, CacheSize: pcsconfig.Config.CacheSize, BlockSize: baidupcs.InitRangeSize, MaxRate: rate, InstanceStateStorageFormat: downloader.InstanceStateStorageFormatProto3, TryHTTP: !pcsconfig.Config.EnableHTTPS}
		executor.Append(&pcsdownload.DownloadTaskUnit{ProbeOnly: kind == "probe", Context: ctx, Progress: onProgress, ExpectedETag: settings.ETag, ExpectedModified: settings.Modified, OnProbe: func(etag, modified string) {
			report(transferProgress{Total: info.Size, ETag: etag, Modified: modified})
		}, OnTransferError: func(err error) {
			if errors.Is(err, downloader.ErrCheckpointSave) {
				report(transferProgress{CheckpointFailed: true})
			}
			if errors.Is(err, downloader.ErrRemoteChanged) || errors.Is(err, downloader.ErrResumeFileMismatch) || errors.Is(err, downloader.ErrResumeSizeMismatch) {
				report(transferProgress{Reset: true})
			}
		}, Cfg: cfg, PCS: pcs, ParentTaskExecutor: executor, DownloadStatistic: statistic, PrintFormat: pcsdownload.DefaultPrintFormat, IsOverwrite: true, NoCheck: true, ModifyMTime: true, DownloadMode: pcsdownload.DownloadModeLocate, PcsPath: args[2], SavePath: args[3], FileInfo: info}, 0)
	} else {
		return errors.New("unknown transfer kind")
	}
	executor.Execute()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if executor.FailedDeque().Size() != 0 {
		return errors.New("transfer failed")
	}
	return nil
}

func workerDownloadInfo(ctx context.Context, pcs *baidupcs.BaiduPCS, remote string) (*baidupcs.FileDirectory, error) {
	info, err := pcs.FilesDirectoriesMetaWithContext(ctx, remote)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRemoteMetadataPath(remote, info); err != nil {
		return nil, err
	}
	if info.Isdir {
		return nil, errors.New("worker expects a file")
	}
	return info, nil
}

// Injectable transfer boundary for account-isolation regression tests.
var runTransferWorker = worker
