package rc

import (
	"bufio"
	"bytes"
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
	"strings"
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
	cmd.Cancel = func() error { _, _ = fmt.Fprintln(input, "cancel"); return nil }
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
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
		return fmt.Errorf("%s transfer failed (account %d); source retained", kind, uid)
	}
	return nil
}

// RunWorker receives paths and an account ID only, never credential arguments.
func RunWorker(args []string) error {
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
	var progressMu sync.Mutex
	report := func(v transferProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()
		b, _ := json.Marshal(v)
		fmt.Fprintf(progressOut, "@pcsclone %s\n", b)
	}
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
		info, e := pcs.FilesDirectoriesMeta(args[2])
		if e != nil {
			return e
		}
		if info.Isdir {
			return errors.New("worker expects a file")
		}
		if kind == "download" {
			if err = os.MkdirAll(filepath.Dir(args[3]), 0755); err != nil {
				return err
			}
		}
		if info.Size == 0 {
			if kind == "probe" {
				report(transferProgress{Total: 0})
				return nil
			}
			f, err := os.Create(args[3])
			if err != nil {
				return err
			}
			return f.Close()
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

// Injectable transfer boundary for account-isolation regression tests.
var runTransferWorker = worker

// Worker stdout contains only this bounded, numeric progress protocol. Legacy
// output and request URLs are never forwarded to the parent terminal.
type workerOutput struct {
	ctx              context.Context
	pending          []byte
	reset            bool
	checkpointFailed bool
}

func (w *workerOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := string(w.pending[:i])
		w.pending = w.pending[i+1:]
		if strings.HasPrefix(line, "@pcsclone ") {
			var v transferProgress
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "@pcsclone ")), &v) == nil {
				w.reset = w.reset || v.Reset
				w.checkpointFailed = w.checkpointFailed || v.CheckpointFailed
				reportProgress(w.ctx, v)
			}
		}
	}
	if len(w.pending) > 64*1024 {
		w.pending = nil
	}
	return n, nil
}
