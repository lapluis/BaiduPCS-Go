package pcscommand

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type exportOutputFixture struct {
	data               bytes.Buffer
	writeErr, closeErr error
	short              bool
	writes, closes     int
}

func (w *exportOutputFixture) Write(p []byte) (int, error) {
	w.writes++
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.short {
		return w.data.Write(p[:len(p)/2])
	}
	return w.data.Write(p)
}

func (w *exportOutputFixture) Close() error { w.closes++; return w.closeErr }

func TestExportOutputErrors(t *testing.T) {
	writeErr := errors.New("fixture write failure")
	closeErr := errors.New("fixture close failure")
	openErr := errors.New("fixture open failure")
	for _, tc := range []struct {
		name                        string
		openErr, writeErr, closeErr error
		short, stdout               bool
		want                        []error
	}{
		{name: "open", openErr: openErr, want: []error{openErr}},
		{name: "write", writeErr: writeErr, want: []error{writeErr}},
		{name: "short write", short: true, want: []error{io.ErrShortWrite}},
		{name: "close", closeErr: closeErr, want: []error{closeErr}},
		{name: "write and close", writeErr: writeErr, closeErr: closeErr, want: []error{writeErr, closeErr}},
		{name: "stdout", stdout: true, writeErr: io.ErrClosedPipe, want: []error{io.ErrClosedPipe}},
		{name: "stdout short write", stdout: true, short: true, want: []error{io.ErrShortWrite}},
		{name: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := exportFixture(t)
			writer := &exportOutputFixture{writeErr: tc.writeErr, closeErr: tc.closeErr, short: tc.short}
			writer.data.WriteString("retained\n")
			opens := 0
			var got error
			log := captureExportCall(t, func() {
				got = runExport([]string{"/fixture.txt", "/fixture.txt"}, &ExportOptions{SavePath: "fixture-output", StdOut: tc.stdout}, writer, func(name string) (io.WriteCloser, error) {
					opens++
					if name != "fixture-output" {
						t.Fatalf("unexpected output: %q", name)
					}
					return writer, tc.openErr
				})
			})
			for _, want := range tc.want {
				if !errors.Is(got, want) {
					t.Errorf("error %v does not retain %v", got, want)
				}
			}
			if len(tc.want) == 0 && got != nil {
				t.Fatal(got)
			}
			wantOpens, wantCloses, wantCalls, wantWrites := 1, 1, 2, 2
			if tc.stdout {
				wantOpens, wantCloses = 0, 0
			}
			if tc.openErr != nil {
				wantCloses, wantCalls, wantWrites = 0, 0, 0
			} else if tc.writeErr != nil || tc.short {
				wantCalls, wantWrites = 1, 1
			}
			if opens != wantOpens || writer.closes != wantCloses || *calls != wantCalls || writer.writes != wantWrites {
				t.Fatalf("opens=%d closes=%d requests=%d writes=%d; want %d/%d/%d/%d", opens, writer.closes, *calls, writer.writes, wantOpens, wantCloses, wantCalls, wantWrites)
			}
			if !strings.HasPrefix(writer.data.String(), "retained\n") {
				t.Fatal("existing output destroyed")
			}
			if tc.writeErr == nil && tc.openErr == nil && !tc.short && strings.Count(writer.data.String(), "BaiduPCS-Go rapidupload") != 2 {
				t.Fatal("valid records lost")
			}
			if got != nil && strings.Contains(log, "导出完毕") {
				t.Fatal("failure reported completion")
			}
		})
	}
}

func TestExportReturnsTaskErrorWithPartialOutput(t *testing.T) {
	calls := exportFixture(t)
	client := GetBaiduPCS().GetClient()
	base := client.Client.Transport
	client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(req)
		if err == nil && *calls == 2 {
			response.Body.Close()
			response.Body = io.NopCloser(strings.NewReader(`{"error_code":31066,"error_msg":"fixture missing"}`))
		}
		return response, err
	})
	writer := &exportOutputFixture{}
	var got error
	log := captureExportCall(t, func() {
		got = runExport([]string{"/fixture.txt", "/missing.txt"}, &ExportOptions{StdOut: true}, writer, func(string) (io.WriteCloser, error) { t.Fatal("stdout opened file"); return nil, nil })
	})
	var remote pcserror.Error
	if !errors.As(got, &remote) || remote.GetRemoteErrCode() != 31066 {
		t.Fatalf("task error lost: %v", got)
	}
	if *calls != 2 || writer.closes != 0 || strings.Count(writer.data.String(), "BaiduPCS-Go rapidupload") != 1 {
		t.Fatalf("partial output lost or retried: calls=%d closes=%d data=%q", *calls, writer.closes, writer.data.String())
	}
	if strings.Contains(log, "导出完毕") {
		t.Fatal("partial failure reported completion")
	}
}
