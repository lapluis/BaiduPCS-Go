package rc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type workerReportFault struct {
	calls, failAt  int
	short, partial bool
	cause          error
}

func (w *workerReportFault) Write(p []byte) (int, error) {
	w.calls++
	if w.calls < w.failAt {
		return len(p), nil
	}
	if w.short {
		return len(p) - 1, nil
	}
	if w.partial {
		return len(p) / 2, w.cause
	}
	return 0, w.cause
}

func TestWorkerReporterRetainsFirstFailureAndCancelsOnce(t *testing.T) {
	for _, mode := range []string{"write-error", "short-write", "partial-error", "after-success"} {
		t.Run(mode, func(t *testing.T) {
			cause := &os.PathError{Op: "write", Path: "synthetic-report-pipe", Err: os.ErrPermission}
			target := &workerReportFault{cause: cause, failAt: 1, short: mode == "short-write", partial: mode == "partial-error"}
			if mode == "after-success" {
				target.failAt = 2
			}
			var cancels atomic.Int32
			r := newWorkerReporter(target, func() { cancels.Add(1) })
			r.Report(transferProgress{Done: 1, Total: 8})
			if mode == "after-success" {
				if r.Err() != nil || cancels.Load() != 0 {
					t.Fatal("successful report canceled transfer")
				}
				r.Report(transferProgress{Done: 2, Total: 8})
			}
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); r.Report(transferProgress{Reset: true, CheckpointFailed: true}) }()
			}
			wg.Wait()
			want := error(cause)
			if mode == "short-write" {
				want = io.ErrShortWrite
			}
			if !errors.Is(r.Err(), want) || target.calls != target.failAt || cancels.Load() != 1 {
				t.Errorf("report failure not sticky: err=%v writes=%d cancels=%d", r.Err(), target.calls, cancels.Load())
			}
		})
	}
}

func TestWorkerReporterPreservesConcurrentProtocolFrames(t *testing.T) {
	var output bytes.Buffer
	canceled := false
	r := newWorkerReporter(&output, func() { canceled = true })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(done int64) {
			defer wg.Done()
			r.Report(transferProgress{Done: done, Total: 32, Reset: done == 0, CheckpointFailed: done == 31})
		}(int64(i))
	}
	wg.Wait()
	seen := map[int64]bool{}
	ctx := context.WithValue(context.Background(), progressKey{}, func(v transferProgress) { seen[v.Done] = true })
	parsed := &workerOutput{ctx: ctx}
	if _, err := parsed.Write(output.Bytes()); err != nil {
		t.Fatal(err)
	}
	if r.Err() != nil || canceled || len(seen) != 32 || !parsed.reset || !parsed.checkpointFailed || len(parsed.pending) != 0 {
		t.Fatalf("concurrent reports damaged protocol: frames=%d err=%v canceled=%t", len(seen), r.Err(), canceled)
	}
}

func TestWorkerReportFailureCancelsTransfer(t *testing.T) {
	for _, kind := range []string{"download", "probe"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerBrokenReportProcessFixture$")
			cmd.Env = append(os.Environ(), "PCS_BROKEN_WORKER_REPORT="+kind)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("worker reporting fixture failed: %v %s", err, output)
			}
		})
	}
}

func TestWorkerBrokenReportProcessFixture(t *testing.T) {
	kind := os.Getenv("PCS_BROKEN_WORKER_REPORT")
	if kind == "" {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Host == "download.fixture.invalid":
			w.Header().Set("X-Bs-File-Size", "8")
			w.Header().Set("ETag", `"fixture-v1"`)
			_, _ = io.WriteString(w, "complete")
		case r.URL.Query().Get("method") == "locatedownload":
			_, _ = io.WriteString(w, `{"error_code":0,"urls":[{"url":"http://download.fixture.invalid/file","encrypt":0}]}`)
		default:
			_, _ = io.WriteString(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","isdir":0,"size":8}]}`)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	cfg := pcsconfig.NewConfig(filepath.Join(root, "config.json"))
	cfg.AppID = 1
	cfg.EnableHTTPS = false
	cfg.PCSAddr = "pcs.baidu.com"
	cfg.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7}}}
	pcsconfig.Config = cfg
	requester.SetGlobalProxy(server.URL)
	requester.SetProxyHostnameRules("")
	path := filepath.Join(root, "data")
	checkpoint := path + pcsdownload.DownloadSuffix
	for name, data := range map[string]string{path: "keepdata", checkpoint: "saved progress"} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	originalStdout := os.Stdout
	os.Stdout = writer
	err = RunWorker([]string{kind, "7", "/fixture", path, "0"})
	os.Stdout = originalStdout
	writer.Close()
	if err == nil {
		t.Error("worker ignored broken progress pipe and returned success")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "write" {
		t.Errorf("report write error lost: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("report failure did not cancel worker: %v", err)
	}
	for name, want := range map[string]string{path: "keepdata", checkpoint: "saved progress"} {
		if got, err := os.ReadFile(name); err != nil || string(got) != want {
			t.Errorf("report failure allowed data modification: %q %v", got, err)
		}
	}
}
