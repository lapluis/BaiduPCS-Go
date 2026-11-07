package pcsdownload

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type locateEnvelopeBody struct {
	*strings.Reader
	closes int
}

func (b *locateEnvelopeBody) Close() error { b.closes++; return nil }

func TestLocateErrorEnvelopeStopsBeforeCandidateProbe(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		code         int
	}{
		{"native", `"error_code":31066`, 31066},
		{"alternate", `"errno":-9`, -9},
		{"alternate-with-zero", `"error_code":0,"errno":-9`, -9},
		{"success", `"error_code":0,"errno":0`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installDownloadClient(t)
			probes := 0
			transport := panClient.Client.Transport
			panClient.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				probes++
				return transport.RoundTrip(req)
			})
			body := &locateEnvelopeBody{Reader: strings.NewReader(fmt.Sprintf(`{%s,"urls":[{"url":"https://eligible.fixture.invalid/file","encrypt":0}]}`, tc.fields))}
			lookups := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				lookups++
				if req.Body != nil {
					defer req.Body.Close()
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: req}, nil
			})
			output := filepath.Join(t.TempDir(), "partial")
			before := map[string]string{output: "precious", output + DownloadSuffix: "saved ranges"}
			for name, data := range before {
				if err := os.WriteFile(name, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			task := emptyTask(output, true, false)
			task.FileInfo.Size, task.ProbeOnly = 8, true
			task.PCS = baidupcs.NewPCSWithClient(1, client)
			task.PCS.SetUID(1)
			result := &taskframework.TaskUnitRunResult{}
			ok := task.locateDownload(result)
			if tc.code != 0 {
				var remote pcserror.Error
				if ok || result.Succeed || result.NeedRetry || probes != 0 || !errors.As(result.Err, &remote) || remote.GetRemoteErrCode() != tc.code {
					t.Fatalf("permanent lookup failure probed, retried or lost error: ok=%v probes=%d result=%+v", ok, probes, result)
				}
			} else if !ok || result.Err != nil || probes != 1 {
				t.Fatalf("healthy lookup changed: ok=%v probes=%d result=%+v", ok, probes, result)
			}
			if lookups != 1 || body.closes != 1 {
				t.Errorf("lookup ownership changed: calls=%d closes=%d", lookups, body.closes)
			}
			for name, want := range before {
				if got, err := os.ReadFile(name); err != nil || string(got) != want {
					t.Errorf("probe changed existing file: %q %v", got, err)
				}
			}
		})
	}
}
