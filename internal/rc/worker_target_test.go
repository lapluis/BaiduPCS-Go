package rc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadWorkerValidatesTargetBeforeRequests(t *testing.T) {
	for _, kind := range []string{"directory", "blocked-parent", "empty-name", "new-file", "existing-file"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			valid := kind == "new-file" || kind == "existing-file"
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "blocked-parent":
				if err := os.WriteFile(target, []byte("retain parent"), 0600); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(target, "child", "data")
			case "new-file":
				target = filepath.Join(target, "child", "data")
			case "empty-name":
				target = ""
			case "existing-file":
				if err := os.WriteFile(target, []byte("old content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			checkDownloadTargetWorker(t, target, valid)
			if valid {
				if data, err := os.ReadFile(target); err != nil || len(data) != 0 {
					t.Fatalf("valid empty download failed: %q %v", data, err)
				}
			} else if kind == "directory" {
				if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
					t.Fatalf("invalid directory target changed: entries=%v err=%v", entries, err)
				}
			} else if kind == "blocked-parent" {
				if data, err := os.ReadFile(filepath.Join(root, "target")); err != nil || string(data) != "retain parent" {
					t.Fatalf("blocking parent changed: %q %v", data, err)
				}
			} else if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("empty target name created local files: %v %v", entries, err)
			}
		})
	}
}

func checkDownloadTargetWorker(t *testing.T, target string, valid bool, duringMetadata ...func()) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if len(duringMetadata) != 0 {
			duringMetadata[0]()
		}
		_, _ = io.WriteString(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/empty","isdir":0,"size":0}]}`)
	}))
	defer server.Close()
	phase := "invalid-target"
	if valid {
		phase = "free"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmptyWorkerProcessFixture$")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PCS_EMPTY_WORKER_OUTPUT="+target, "PCS_EMPTY_WORKER_PROXY="+server.URL, "PCS_EMPTY_WORKER_PHASE="+phase)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("download target worker failed: %v %s", err, output)
	}
	want := int32(0)
	if valid || len(duringMetadata) != 0 {
		want = 1
	}
	if calls.Load() != want {
		t.Errorf("target validation did not precede requests: calls=%d want=%d", calls.Load(), want)
	}
}
