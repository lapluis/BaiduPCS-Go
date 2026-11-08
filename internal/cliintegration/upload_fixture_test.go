package cliintegration

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type uploadCLIExecutable struct {
	binary, rootPEM string
	certificate     tls.Certificate
}

func (cli *uploadCLIExecutable) proxy(t *testing.T, handler http.Handler) string {
	t.Helper()
	tlsServer := httptest.NewUnstartedServer(handler)
	tlsServer.TLS = &tls.Config{Certificates: []tls.Certificate{cli.certificate}}
	tlsServer.StartTLS()
	var mu sync.Mutex
	var tunnels []net.Conn
	var closing bool
	var joined sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			handler.ServeHTTP(w, r)
			return
		}
		if r.Host != "pan.baidu.com:443" {
			t.Errorf("unexpected fixture CONNECT: %s", r.Host)
			http.Error(w, "unexpected CONNECT", http.StatusForbidden)
			return
		}
		// The tunnel always terminates at the local fixture, never a real host.
		upstream, err := net.DialTimeout("tcp", tlsServer.Listener.Addr().String(), 2*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		mu.Lock()
		if closing {
			mu.Unlock()
			return
		}
		tunnels = append(tunnels, client, upstream)
		joined.Add(1)
		mu.Unlock()
		defer joined.Done()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	t.Cleanup(func() {
		mu.Lock()
		closing = true
		for _, conn := range tunnels {
			_ = conn.Close()
		}
		mu.Unlock()
		server.Close()
		tlsServer.Close()
		joined.Wait()
	})
	return server.URL
}

func (cli *uploadCLIExecutable) run(t *testing.T, root, mode, source, proxy string) ([]byte, error) {
	t.Helper()
	config := filepath.Join(root, "pcs_config.json")
	data := fmt.Sprintf(`{"appid":1,"pcs_addr":"pcs.baidu.com","enable_https":false,"cache_size":8192,"proxy":%q,"baidu_active_uid":7,"baidu_user_list":[{"uid":7}]}`, proxy)
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rc_remotes.json"), []byte(`{"bd":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli.binary, "--config", config, mode, source, "bd:/target", "-P", "--retries", "0", "--transfers", "1")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "BAIDUPCS_GO_CONFIG_DIR="+root, "BAIDUPCS_GO_VERBOSE=0", "PCS_TEST_UPLOAD_CLI=1", "PCS_TEST_UPLOAD_ROOT="+cli.rootPEM, "GODEBUG="+os.Getenv("GODEBUG")+",x509usefallbackroots=1")
	cmd.Env = childRaceEnv(t, cmd.Env)
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("fixture CLI timed out: %v\n%s", ctx.Err(), output)
	}
	return output, err
}

func serveUploadVerificationContent(t *testing.T, w http.ResponseWriter, r *http.Request, content []byte) {
	t.Helper()
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", `"fixture-content"`)
	begin, end := 0, len(content)-1
	if header := r.Header.Get("Range"); header != "" {
		if _, err := fmt.Sscanf(header, "bytes=%d-%d", &begin, &end); err != nil || begin < 0 || begin >= len(content) || end < begin {
			t.Error("invalid verification range", header)
			http.Error(w, "invalid range", 416)
			return
		}
		if end >= len(content) {
			end = len(content) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(content)))
		w.Header().Set("Content-Length", fmt.Sprint(end-begin+1))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
	}
	w.Write(content[begin : end+1])
}
