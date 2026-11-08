package pcscommand

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The fixture has a single empty directory, either selected directly or under
// a parent. No account configuration or real directory listing is loaded.
func exportDirectoryFixture(t *testing.T, nested bool) *int {
	t.Helper()
	exportFixture(t)
	calls := new(int)
	client := GetBaiduPCS().GetClient()
	client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
		*calls++
		if req.Body != nil {
			defer req.Body.Close()
		}
		q := req.URL.Query()
		payload := `{"errno":0,"list":[]}`
		switch {
		case q.Get("method") == "meta":
			payload = `{"error_code":0,"list":[{"fs_id":1,"path":"/tree","isdir":1}]}`
		case req.URL.Path == "/api/list":
			if nested && q.Get("dir") == "/tree" && q.Get("page") == "1" {
				payload = `{"errno":0,"list":[{"fs_id":2,"path":"/tree/empty","server_filename":"empty","isdir":1}]}`
			} else if q.Get("dir") != "/tree" && q.Get("dir") != "/tree/empty" {
				t.Fatalf("unexpected directory: %s", req.URL)
			}
		default:
			t.Fatalf("unexpected request: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	})
	return calls
}

func TestExportEmptyDirectories(t *testing.T) {
	for _, shape := range []string{"direct", "recursive", "nonrecursive"} {
		for _, stdout := range []bool{false, true} {
			for _, link := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stdout=%t/link=%t", shape, stdout, link), func(t *testing.T) {
					calls := exportDirectoryFixture(t, shape != "direct")
					writer := &exportOutputFixture{}
					opens := 0
					var got error
					log := captureExportCall(t, func() {
						got = runExport([]string{"/tree"}, &ExportOptions{StdOut: stdout, LinkFormat: link, Recursive: shape == "recursive", RootPath: "/relocated", SavePath: "fixture-output"}, writer, func(string) (io.WriteCloser, error) { opens++; return writer, nil })
					})
					if got != nil {
						t.Fatal(got)
					}
					want := "BaiduPCS-Go mkdir \"/relocated/tree\"\n"
					wantCalls := 2
					if shape == "recursive" {
						want = "BaiduPCS-Go mkdir \"/relocated/tree/empty\"\n"
						wantCalls = 3
					}
					if shape == "nonrecursive" {
						want = ""
					}
					if writer.data.String() != want {
						t.Fatalf("directory records=%q, want %q", writer.data.String(), want)
					}
					if *calls != wantCalls {
						t.Fatalf("requests=%d want %d", *calls, wantCalls)
					}
					wantOpens := 1
					if stdout {
						wantOpens = 0
					}
					if opens != wantOpens || writer.closes != wantOpens {
						t.Fatalf("opens=%d closes=%d want %d", opens, writer.closes, wantOpens)
					}
					if stdout && !strings.Contains(log, "导出完毕") {
						t.Fatal("missing completion")
					}
				})
			}
		}
	}
}

func TestExportEmptyDirectoryWriteErrors(t *testing.T) {
	for _, stdout := range []bool{false, true} {
		for _, short := range []bool{false, true} {
			t.Run(fmt.Sprintf("stdout=%t/short=%t", stdout, short), func(t *testing.T) {
				exportDirectoryFixture(t, false)
				want := error(io.ErrClosedPipe)
				writer := &exportOutputFixture{writeErr: want}
				if short {
					writer.writeErr = nil
					writer.short = true
					want = io.ErrShortWrite
				}
				var got error
				log := captureExportCall(t, func() {
					got = runExport([]string{"/tree"}, &ExportOptions{StdOut: stdout, SavePath: "fixture-output"}, writer, func(string) (io.WriteCloser, error) { return writer, nil })
				})
				if !errors.Is(got, want) {
					t.Fatalf("got %v want %v", got, want)
				}
				if writer.writes != 1 || strings.Contains(log, "导出成功") || strings.Contains(log, "导出完毕") {
					t.Fatalf("incorrect success: writes=%d log=%q", writer.writes, log)
				}
			})
		}
	}
}
