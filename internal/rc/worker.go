package rc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/multipartreader"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsupload"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

// Worker processes isolate legacy global account clients and stdout, and can be
// cancelled without allowing a partially completed transfer to trigger deletion.
func worker(ctx context.Context, kind string, uid uint64, src, dst string, rate int64) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, "__rc_transfer", kind, strconv.FormatUint(uid, 10), src, dst, strconv.FormatInt(rate, 10))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s transfer failed (account %d); source retained", kind, uid)
	}
	return nil
}

// RunWorker receives paths and an account ID only, never credential arguments.
func RunWorker(args []string) error {
	if len(args) != 5 {
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
			body, e := pcs.PrepareUpload(baidupcs.OverWritePolicy, args[3], func(url string, jar http.CookieJar) (*http.Response, error) {
				mr := multipartreader.NewMultipartReader()
				mr.AddFormFile("file", "empty", pcsupload.EmptyReaderLen64{})
				mr.CloseMultipart()
				client := pcsconfig.Config.PCSHTTPClient()
				client.SetCookiejar(jar)
				return client.Req(http.MethodPost, url, mr, nil)
			})
			if e != nil {
				return e
			}
			defer body.Close()
			return pcserror.DecodePCSJSONError(baidupcs.OperationUpload, body)
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
		executor.Append(&pcsupload.UploadTaskUnit{IgnoreCachedMatch: true, LocalFileChecksum: checksum.NewLocalFileChecksum(args[2], int(baidupcs.SliceMD5Size)), SavePath: args[3], PCS: pcs, UploadingDatabase: db, Parallel: 1, PrintFormat: pcsupload.DefaultPrintFormat, UploadStatistic: statistic, Policy: baidupcs.OverWritePolicy}, 0)
	} else if kind == "download" {
		info, e := pcs.FilesDirectoriesMeta(args[2])
		if e != nil {
			return e
		}
		if info.Isdir {
			return errors.New("worker expects a file")
		}
		if err = os.MkdirAll(filepath.Dir(args[3]), 0755); err != nil {
			return err
		}
		if info.Size == 0 {
			f, err := os.Create(args[3])
			if err != nil {
				return err
			}
			return f.Close()
		}
		statistic := &pcsdownload.DownloadStatistic{}
		statistic.StartTimer()
		cfg := &downloader.Config{Mode: transfer.RangeGenMode_BlockSize, MaxParallel: 1, CacheSize: pcsconfig.Config.CacheSize, BlockSize: baidupcs.InitRangeSize, MaxRate: rate, InstanceStateStorageFormat: downloader.InstanceStateStorageFormatProto3, TryHTTP: !pcsconfig.Config.EnableHTTPS}
		executor.Append(&pcsdownload.DownloadTaskUnit{Cfg: cfg, PCS: pcs, ParentTaskExecutor: executor, DownloadStatistic: statistic, PrintFormat: pcsdownload.DefaultPrintFormat, IsOverwrite: true, NoCheck: true, ModifyMTime: true, DownloadMode: pcsdownload.DownloadModeLocate, PcsPath: args[2], SavePath: args[3], FileInfo: info}, 0)
	} else {
		return errors.New("unknown transfer kind")
	}
	executor.Execute()
	if executor.FailedDeque().Size() != 0 {
		return errors.New("transfer failed")
	}
	return nil
}

// Injectable transfer boundary for account-isolation regression tests.
var runTransferWorker = worker
