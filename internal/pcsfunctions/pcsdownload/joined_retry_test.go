package pcsdownload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func joinedRetryRemote(code int) error {
	return pcserror.DecodePCSJSONError("fixture download", strings.NewReader(fmt.Sprintf(`{"error_code":%d}`, code)))
}

func TestJoinedDownloadRetryHonorsEveryCause(t *testing.T) {
	pathErr := &os.PathError{Op: "write", Path: "fixture", Err: os.ErrPermission}
	for _, tc := range []struct {
		name  string
		cause error
		retry bool
	}{
		{"local", pathErr, false},
		{"wrapped-local", fmt.Errorf("output: %w", pathErr), false},
		{"missing-user", joinedRetryRemote(31045), false},
		{"missing-file", joinedRetryRemote(31066), false},
		{"missing-file-alternative", joinedRetryRemote(31297), false},
		{"pan-missing-file", pcserror.DecodePanJSONError("fixture download", strings.NewReader(`{"errno":-9}`)), false},
		{"canceled", context.Canceled, false},
		{"checkpoint", downloader.ErrCheckpointSave, false},
		{"progress", downloader.ErrProgressCallback, false},
		{"lifecycle", downloader.ErrEventCallback, false},
		{"network", errors.New("transient failure"), true},
		{"deadline", context.DeadlineExceeded, true},
		{"retryable-remote", joinedRetryRemote(31626), true},
	} {
		for _, reverse := range []bool{false, true} {
			for _, wrapping := range []string{"direct", "wrapped", "pcs", "pcs-remote"} {
				t.Run(fmt.Sprintf("%s/reverse=%t/%s", tc.name, reverse, wrapping), func(t *testing.T) {
					transient := joinedRetryRemote(31626)
					joined := errors.Join(transient, tc.cause)
					if reverse {
						joined = errors.Join(tc.cause, transient)
					}
					var input error = joined
					if wrapping == "wrapped" {
						input = fmt.Errorf("transfer: %w", input)
					}
					if wrapping == "pcs" {
						pcs := pcserror.NewPCSErrorInfo("fixture network")
						pcs.SetNetError(input)
						input = pcs
					}
					if wrapping == "pcs-remote" {
						pcs := pcserror.NewPCSErrorInfo("fixture remote")
						pcs.ErrCode, pcs.Err = 31626, input
						pcs.SetRemoteError()
						input = pcs
					}
					for _, initial := range []bool{false, true} {
						result := &taskframework.TaskUnitRunResult{Err: input, NeedRetry: initial}
						(&DownloadTaskUnit{}).handleError(result)
						if result.NeedRetry != tc.retry || result.Err != input || !errors.Is(result.Err, transient) || !errors.Is(result.Err, tc.cause) {
							t.Fatalf("joined error changed retry policy or lost causes: retry=%t want=%t error=%v", result.NeedRetry, tc.retry, result.Err)
						}
					}
				})
			}
		}
	}
}

func TestDownloadErrorCallbackPermanentCauseStopsRetry(t *testing.T) {
	for _, mode := range []string{"local", "remote-missing"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(path, []byte("keep original"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+DownloadSuffix, []byte("keep ranges"), 0600); err != nil {
				t.Fatal(err)
			}
			task := emptyTask(path, true, false)
			task.FileInfo.Size = 8
			transient := joinedRetryRemote(31626)
			var permanent error = &os.PathError{Op: "write", Path: path, Err: os.ErrPermission}
			if mode == "remote-missing" {
				permanent = joinedRetryRemote(31066)
			}
			calls, reports := 0, 0
			client := requester.NewHTTPClient()
			client.Client.Transport = downloadRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, transient })
			task.OnTransferError = func(error) { reports++; panic(permanent) }
			err, escaped := invokeDownloadWithPanicCapture(task, client)
			if escaped != nil || calls != 1 || reports != 1 || !errors.Is(err, transient) || !errors.Is(err, permanent) {
				t.Fatalf("callback did not preserve both causes: escaped=%v calls=%d reports=%d error=%v", escaped, calls, reports, err)
			}
			result := &taskframework.TaskUnitRunResult{Err: err, NeedRetry: true}
			task.handleError(result)
			if result.NeedRetry {
				t.Errorf("permanent callback cause was hidden by a retryable remote error: %v", err)
			}
			for name, want := range map[string]string{path: "keep original", path + DownloadSuffix: "keep ranges"} {
				if got, err := os.ReadFile(name); err != nil || string(got) != want {
					t.Fatalf("error handling changed data: %q %v", got, err)
				}
			}
		})
	}
}
