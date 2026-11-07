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

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func invokeDownloadWithPanicCapture(unit *DownloadTaskUnit, client *requester.HTTPClient) (err error, escaped any) {
	defer func() { escaped = recover() }()
	err = unit.download("https://fixture.invalid/file", client)
	return
}

func TestDownloadErrorCallbackPanicRetainsOriginalCause(t *testing.T) {
	for _, cause := range []error{
		errors.New("synthetic network failure"),
		&os.PathError{Op: "open", Path: "synthetic", Err: os.ErrPermission},
		context.Canceled,
		errors.Join(context.Canceled, downloader.ErrCheckpointSave),
	} {
		for _, typed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%t", cause, typed), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "data")
				if err := os.WriteFile(path, []byte("keep original"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+DownloadSuffix, []byte("saved progress"), 0600); err != nil {
					t.Fatal(err)
				}
				unit := emptyTask(path, true, false)
				unit.FileInfo.Size = 8
				client := requester.NewHTTPClient()
				client.Client.Transport = downloadRoundTrip(func(*http.Request) (*http.Response, error) { return nil, cause })
				panicCause := errors.New("synthetic error callback panic")
				var original error
				unit.OnTransferError = func(err error) {
					original = err
					if typed {
						panic(panicCause)
					}
					panic("synthetic error callback panic")
				}
				err, escaped := invokeDownloadWithPanicCapture(unit, client)
				if escaped != nil {
					t.Fatalf("callback panic escaped: %v", escaped)
				}
				if original == nil || !errors.Is(err, cause) {
					t.Errorf("callback panic hid original cause: %v", err)
				}
				if typed && !errors.Is(err, panicCause) {
					t.Errorf("typed panic cause lost: %v", err)
				}
				if err == nil || !strings.Contains(err.Error(), panicCause.Error()) {
					t.Errorf("panic diagnostic lost: %v", err)
				}
				if errors.Is(err, downloader.ErrCheckpointSave) != errors.Is(cause, downloader.ErrCheckpointSave) || errors.Is(err, context.Canceled) != errors.Is(cause, context.Canceled) {
					t.Errorf("callback panic changed checkpoint/cancellation classification: %v", err)
				}
				var actualPath *os.PathError
				if _, ok := cause.(*os.PathError); ok && !errors.As(err, &actualPath) {
					t.Errorf("local path error lost: %v", err)
				}
				expected, actual := &taskframework.TaskUnitRunResult{Err: original}, &taskframework.TaskUnitRunResult{Err: err}
				unit.handleError(expected)
				unit.handleError(actual)
				if actual.NeedRetry != expected.NeedRetry {
					t.Errorf("callback panic changed retry policy: got=%t expected=%t", actual.NeedRetry, expected.NeedRetry)
				}
				for name, want := range map[string]string{path: "keep original", path + DownloadSuffix: "saved progress"} {
					if got, err := os.ReadFile(name); err != nil || string(got) != want {
						t.Errorf("panic changed saved data: %q %v", got, err)
					}
				}
			})
		}
	}
}

func TestDownloadProbePanicClosesResponseInEveryMode(t *testing.T) {
	for _, mode := range []string{"download", "test", "probe"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data")
			unit := emptyTask(path, true, mode == "test")
			unit.FileInfo.Size = 8
			unit.ProbeOnly = mode == "probe"
			cause := errors.New("synthetic probe callback panic")
			unit.OnProbe = func(string, string) { panic(cause) }
			body := &probeBody{}
			client := requester.NewHTTPClient()
			client.Client.Transport = downloadRoundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, ContentLength: 8, Header: make(http.Header), Body: body, Request: r}, nil
			})
			err, escaped := invokeDownloadWithPanicCapture(unit, client)
			if escaped != nil {
				t.Errorf("probe panic escaped task boundary: %v", escaped)
			}
			if !errors.Is(err, cause) {
				t.Errorf("probe panic cause lost: %v", err)
			}
			if !body.closed || body.read {
				t.Errorf("panic leaked or consumed response: closed=%t read=%t", body.closed, body.read)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("probe panic left empty output: %v", err)
			}
		})
	}
}
