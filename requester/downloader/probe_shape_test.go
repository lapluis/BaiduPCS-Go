package downloader

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestIncompleteProbeResponseDoesNotPanicOrChangeOutput(t *testing.T) {
	for _, shape := range []string{"nil-response", "nil-body", "nil-request", "nil-request-url", "valid"} {
		for _, probeError := range []bool{false, true} {
			for _, contextual := range []bool{false, true} {
				t.Run(shape+map[bool]string{false: "/success", true: "/error"}[probeError]+map[bool]string{false: "/legacy", true: "/context"}[contextual], func(t *testing.T) {
					file, err := os.Create(filepath.Join(t.TempDir(), "output"))
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					if _, err := file.WriteString("existing output"); err != nil {
						t.Fatal(err)
					}
					body := &ownedProbeBody{Reader: strings.NewReader("abcdefgh")}
					cause := errors.New("fixture probe failure")
					probe := func(_ *requester.HTTPClient, uri string) (int64, *http.Response, error) {
						req, err := http.NewRequest(http.MethodGet, uri, nil)
						if err != nil {
							t.Fatal(err)
						}
						resp := &http.Response{StatusCode: http.StatusOK, ContentLength: 8, Body: body, Request: req}
						switch shape {
						case "nil-response":
							resp = nil
						case "nil-body":
							resp.Body = nil
						case "nil-request":
							resp.Request = nil
						case "nil-request-url":
							resp.Request.URL = nil
						}
						if probeError {
							return 8, resp, cause
						}
						return 8, resp, nil
					}
					client := requester.NewHTTPClient()
					client.Client.Transport = queuedWriteTransport(func(*http.Request) (*http.Response, error) {
						t.Error("incomplete probe started another request")
						return nil, errors.New("unexpected network request")
					})
					d := NewDownloader("https://probe.fixture.invalid/file", file, &Config{MaxParallel: 1, CacheSize: 1024})
					d.SetClient(client)
					if contextual {
						d.SetDURLCheckFuncWithContext(func(_ context.Context, client *requester.HTTPClient, uri string) (int64, *http.Response, error) {
							return probe(client, uri)
						})
					} else {
						d.SetDURLCheckFunc(probe)
					}
					executed, succeeded := false, false
					d.OnExecute(func() { executed = true })
					d.OnSuccess(func() { succeeded = true })
					func() {
						defer func() {
							if p := recover(); p != nil {
								t.Errorf("incomplete probe panicked: %v", p)
							}
						}()
						err = d.ExecuteWithContext(context.Background())
					}()
					valid := !probeError && shape != "nil-response" && shape != "nil-body"
					if probeError && !errors.Is(err, cause) || !probeError && !valid && (err == nil || !strings.Contains(err.Error(), "probe")) || valid && err != nil {
						t.Errorf("wrong probe result: %v", err)
					}
					want := "existing output"
					if valid {
						want = "abcdefgh"
					}
					data, readErr := os.ReadFile(file.Name())
					if readErr != nil || string(data) != want || executed != valid || succeeded != valid {
						t.Errorf("wrong transfer result: data=%q err=%v execute=%t success=%t", data, readErr, executed, succeeded)
					}
					closes := int32(1)
					if shape == "nil-response" || shape == "nil-body" {
						closes = 0
					}
					if body.closes.Load() != closes || !valid && body.reads.Load() != 0 {
						t.Errorf("wrong body ownership: closes=%d reads=%d", body.closes.Load(), body.reads.Load())
					}
				})
			}
		}
	}
}

func TestIncompleteMirrorProbeIsNotScheduled(t *testing.T) {
	for _, shape := range []string{"nil-response", "nil-body", "nil-request", "nil-request-url", "valid"} {
		for _, probeError := range []bool{false, true} {
			for _, contextual := range []bool{false, true} {
				t.Run(shape+map[bool]string{false: "/success", true: "/error"}[probeError]+map[bool]string{false: "/legacy", true: "/context"}[contextual], func(t *testing.T) {
					d := NewDownloader("https://primary.fixture.invalid/file", nil, NewConfig())
					d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8})
					d.AddLoadBalanceServer("https://mirror.fixture.invalid/file")
					body := &ownedProbeBody{Reader: strings.NewReader("abcdefgh")}
					probe := func(_ *requester.HTTPClient, uri string) (int64, *http.Response, error) {
						req, err := http.NewRequest(http.MethodGet, uri, nil)
						if err != nil {
							t.Error(err)
							return 0, nil, err
						}
						resp := &http.Response{StatusCode: http.StatusOK, ContentLength: 8, Body: body, Request: req}
						switch shape {
						case "nil-response":
							resp = nil
						case "nil-body":
							resp.Body = nil
						case "nil-request":
							resp.Request = nil
						case "nil-request-url":
							resp.Request.URL = nil
						}
						if probeError {
							return 8, resp, errors.New("fixture mirror failure")
						}
						return 8, resp, nil
					}
					if contextual {
						d.SetDURLCheckFuncWithContext(func(_ context.Context, client *requester.HTTPClient, uri string) (int64, *http.Response, error) {
							return probe(client, uri)
						})
					} else {
						d.SetDURLCheckFunc(probe)
					}
					d.lazyInit()
					servers := d.checkLoadBalancers(context.Background())
					want := 1
					if shape == "valid" && !probeError {
						want++
					}
					if len(servers.lbr) != want || servers.lbr[0].URL != d.durl || want == 2 && servers.lbr[1].URL != "https://mirror.fixture.invalid/file" {
						t.Errorf("incomplete mirror was scheduled: %+v", servers.lbr)
					}
					closes := int32(1)
					if shape == "nil-response" || shape == "nil-body" {
						closes = 0
					}
					if body.closes.Load() != closes || body.reads.Load() != 0 {
						t.Errorf("wrong mirror body ownership: closes=%d reads=%d", body.closes.Load(), body.reads.Load())
					}
				})
			}
		}
	}
}
