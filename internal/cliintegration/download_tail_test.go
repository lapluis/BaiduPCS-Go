package cliintegration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real worker stop, checkpoint flush, fresh locate and resumed
// HTTP Range requests. Trust the fixture CA only inside the test processes.
func TestCLISlowTailReconnect(t *testing.T) {
	cli := newUploadCLIExecutable(t)
	payload := make([]byte, 64<<20)
	for i := range payload {
		payload[i] = byte(i*31 + i/257)
	}
	meta := func(path string) map[string]any {
		id, dir, size := 7, 0, len(payload)
		switch path {
		case "/batch":
			dir, size = 1, 0
		case "/batch/fast":
			id = 8
		case "/batch/tail":
			id = 9
		}
		return map[string]any{"fs_id": id, "path": path, "isdir": dir, "size": size, "mtime": 1700000000, "server_mtime": 1700000000}
	}
	var locates atomic.Int32
	var resumedBytes atomic.Int64
	proxy := cli.proxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case r.URL.Path == "/api/list":
			json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": []any{meta("/batch/fast"), meta("/batch/tail")}})
		case query.Get("method") == "meta":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, "bad form", 400)
				return
			}
			var param struct{ List []struct{ Path string } }
			if err := json.Unmarshal([]byte(r.FormValue("param")), &param); err != nil || len(param.List) != 1 {
				http.Error(w, "bad metadata request", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"error_code": 0, "list": []any{meta(param.List[0].Path)}})
		case query.Get("method") == "locatedownload":
			name := "fast"
			if query.Get("path") == "/batch/tail" {
				name = fmt.Sprintf("tail-%d", locates.Add(1))
			}
			fmt.Fprintf(w, `{"error_code":0,"urls":[{"url":"http://cdn.fixture.invalid/%s"}]}`, name)
		case r.Host == "cdn.fixture.invalid":
			begin, end := 0, len(payload)-1
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("X-Bs-File-Size", fmt.Sprint(len(payload)))
			code := http.StatusOK
			if header := r.Header.Get("Range"); header != "" {
				if _, err := fmt.Sscanf(header, "bytes=%d-%d", &begin, &end); err != nil || begin < 0 || end >= len(payload) || end < begin {
					http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(payload)))
				code = http.StatusPartialContent
			}
			w.Header().Set("Content-Length", fmt.Sprint(end-begin+1))
			w.WriteHeader(code)
			if r.Method == http.MethodHead {
				return
			}
			for begin <= end {
				next := min(begin+64*1024, end+1)
				n, err := w.Write(payload[begin:next])
				if r.URL.Path == "/tail-2" {
					resumedBytes.Add(int64(n))
				}
				if err != nil {
					return
				}
				w.(http.Flusher).Flush()
				begin = next
				delay := 8 * time.Millisecond
				if r.URL.Path == "/tail-1" {
					delay = 250 * time.Millisecond
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(delay):
				}
			}
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	dir := t.TempDir()
	cfg := filepath.Join(dir, "pcs_config.json")
	config := fmt.Sprintf(`{"appid":1,"pcs_addr":"pcs.baidu.com","enable_https":false,"cache_size":8192,"proxy":%q,"baidu_active_uid":7,"baidu_user_list":[{"uid":7}]}`, proxy)
	if err := os.WriteFile(cfg, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rc_remotes.json"), []byte(`{"bd":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	target := filepath.Join(dir, "out")
	cmd := exec.CommandContext(ctx, cli.binary, "--config", cfg, "copy", "bd:/batch", target, "--transfers", "2", "--download-threads", "1", "--max-connections", "2", "--retries", "1", "--use-json-log")
	cmd.Env = childRaceEnv(t, append(os.Environ(), "PCS_TEST_UPLOAD_CLI=1", "PCS_TEST_UPLOAD_ROOT="+cli.rootPEM, "BAIDUPCS_GO_VERBOSE=0", "GODEBUG="+os.Getenv("GODEBUG")+",x509usefallbackroots=1"))
	output, err := cmd.CombinedOutput()
	if err != nil || bytes.Count(output, []byte(`"event":"reconnect"`)) != 1 || locates.Load() != 2 || resumedBytes.Load() <= 0 || resumedBytes.Load() >= int64(len(payload)) || bytes.Count(output, []byte(`"event":"copy"`)) != 2 {
		t.Fatalf("reconnect: %v locates=%d resumed bytes=%d\n%s", err, locates.Load(), resumedBytes.Load(), output)
	}
	for _, name := range []string{"fast", "tail"} {
		data, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || sha256.Sum256(data) != sha256.Sum256(payload) {
			t.Fatalf("bad %s: %v", name, err)
		}
	}
	t.Logf("one automatic reconnect; second connection served %d of %d bytes; both SHA256 checks passed", resumedBytes.Load(), len(payload))
}
