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
	"time"

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

func TestDownloadCallbackPanicRetainsConcurrentTaskCancellation(t *testing.T) {
	for _, mode := range []string{"download", "test", "probe"} {
		for _, callback := range []string{"probe", "error"} {
			if mode == "probe" && callback == "error" {
				continue
			}
			for _, existing := range []bool{false, true} {
				for _, typed := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/existing=%v/typed=%v", mode, callback, existing, typed), func(t *testing.T) {
						name := filepath.Join(t.TempDir(), "nested", "output")
						if existing {
							if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(name, []byte("keep original data"), 0600); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(name+DownloadSuffix, []byte("keep original ranges"), 0600); err != nil {
								t.Fatal(err)
							}
						}
						unit := emptyTask(name, true, mode == "test")
						unit.FileInfo.Size = 8
						unit.ProbeOnly = mode == "probe"
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						unit.Context = ctx
						panicCause := errors.New("synthetic canceled callback panic")
						networkCause := errors.New("synthetic request failure")
						calls := 0
						fail := func() {
							calls++
							cancel()
							if typed {
								panic(panicCause)
							}
							panic(panicCause.Error())
						}
						if callback == "probe" {
							unit.OnProbe = func(string, string) { fail() }
						} else {
							unit.OnTransferError = func(error) { fail() }
						}
						body := &probeBody{}
						client := requester.NewHTTPClient()
						client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
							if callback == "error" {
								return nil, networkCause
							}
							return &http.Response{StatusCode: 200, ContentLength: 8, Header: http.Header{}, Body: body, Request: req}, nil
						})
						err, escaped := invokeDownloadWithPanicCapture(unit, client)
						if escaped != nil || calls != 1 || !errors.Is(err, context.Canceled) || err == nil || !strings.Contains(err.Error(), panicCause.Error()) || errors.Is(err, panicCause) != typed {
							t.Fatalf("concurrent cancellation/callback cause lost: escaped=%v calls=%d err=%v", escaped, calls, err)
						}
						if callback == "error" && !errors.Is(err, networkCause) {
							t.Fatalf("original transfer error lost: %v", err)
						}
						if callback == "probe" && (!body.closed || body.read) {
							t.Fatalf("callback leaked or consumed response: %+v", body)
						}
						if existing {
							for path, want := range map[string]string{name: "keep original data", name + DownloadSuffix: "keep original ranges"} {
								if got, err := os.ReadFile(path); err != nil || string(got) != want {
									t.Fatalf("callback changed existing data: %q %v", got, err)
								}
							}
						} else if _, err := os.Stat(name); !os.IsNotExist(err) {
							t.Fatalf("callback left output file: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestPreCanceledDownloadDoesNotPrepareLocalOutput(t *testing.T) {
	for _, mode := range []string{"download", "test", "probe"} {
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expired=%v", mode, expired), func(t *testing.T) {
				name := filepath.Join(t.TempDir(), "missing", "output")
				unit := emptyTask(name, true, mode == "test")
				unit.ProbeOnly = mode == "probe"
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var want error = context.Canceled
				if expired {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer cancel()
					want = context.DeadlineExceeded
				} else {
					cancel()
				}
				unit.Context = ctx
				requests, callbacks := 0, 0
				unit.OnProbe = func(string, string) { callbacks++ }
				unit.OnTransferError = func(error) { callbacks++ }
				client := requester.NewHTTPClient()
				client.Client.Transport = downloadRoundTrip(func(*http.Request) (*http.Response, error) {
					requests++
					return nil, errors.New("unexpected request after cancellation")
				})
				err := unit.download("https://fixture.invalid/file", client)
				if !errors.Is(err, want) || requests != 0 || callbacks != 0 {
					t.Fatalf("pre-canceled download did work: err=%v requests=%d callbacks=%d", err, requests, callbacks)
				}
				if _, err := os.Stat(filepath.Dir(name)); !os.IsNotExist(err) {
					t.Fatalf("pre-canceled download created directory: %v", err)
				}
			})
		}
	}
}
