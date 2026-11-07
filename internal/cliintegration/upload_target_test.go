package cliintegration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsclone"
)

// The fixture executable delegates to the production entry point and launches
// ordinary worker processes. Only these subprocesses trust the synthetic TLS CA;
// neither the product binary nor the machine certificate store is changed.
func TestMain(m *testing.M) {
	if os.Getenv("PCS_TEST_UPLOAD_CLI") == "1" {
		certificate, err := base64.StdEncoding.DecodeString(os.Getenv("PCS_TEST_UPLOAD_ROOT"))
		roots := x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(certificate) {
			fmt.Fprintln(os.Stderr, "invalid upload fixture root")
			os.Exit(2)
		}
		x509.SetFallbackRoots(roots)
		os.Exit(pcsclone.Run(os.Args[1:], "upload-fixture", os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func newUploadCLIExecutable(t *testing.T) *uploadCLIExecutable {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pcsclone-test.exe")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer buildCancel()
	build := exec.CommandContext(buildCtx, "go", "test", "-c", "-o", binary, "./internal/cliintegration")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "upload fixture"},
		DNSNames: []string{"pan.baidu.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rootPEM := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return &uploadCLIExecutable{binary: binary, rootPEM: rootPEM, certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}}
}

func TestCLIUploadTargetFailurePreservesSource(t *testing.T) {
	cli := newUploadCLIExecutable(t)
	for _, fixture := range []struct{ name, payload string }{
		{"pcs-error", `{"error_code":31064}`},
		{"pan-error", `{"errno":-6}`},
		{"zero-native-pan-error", `{"error_code":0,"errno":-6}`},
		{"malformed-with-skip-code", `{"error_code":114514,"list":"invalid"}`},
		{"wrong-path", `{"error_code":0,"list":[{"fs_id":1,"path":"/other","size":8}]}`},
		{"invalid-directory-flag", `{"error_code":0,"list":[{"fs_id":1,"path":"/target","size":8,"isdir":2}]}`},
	} {
		for _, mode := range []string{"copyto", "moveto"} {
			t.Run(fixture.name+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				source := filepath.Join(root, "source")
				if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(1700000000, 0)
				if err := os.Chtimes(source, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				var workerReady atomic.Bool
				var targetFailures, mutations atomic.Int32
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Body != nil {
						_, _ = io.Copy(io.Discard, r.Body)
						_ = r.Body.Close()
					}
					switch {
					case r.URL.Query().Get("method") == "meta":
						if workerReady.Load() {
							targetFailures.Add(1)
							io.WriteString(w, fixture.payload)
						} else {
							// Planning and pre-transfer checks see an absent target.
							io.WriteString(w, `{"error_code":31066}`)
						}
					case r.URL.Path == "/api/list":
						io.WriteString(w, `{"errno":0,"list":[]}`)
					case r.URL.Path == "/api/user/getinfo":
						workerReady.Store(true)
						io.WriteString(w, `{"errno":0,"records":[{"uk":7}]}`)
					default:
						mutations.Add(1)
						// Never forward a request to a real service, including CONNECT.
						io.WriteString(w, `{"errno":0,"return_type":2}`)
					}
				})
				proxy := cli.proxy(t, handler)
				output, runErr := cli.run(t, root, mode, source, proxy)
				var exit *exec.ExitError
				if !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("expected normal failure exit: err=%v output=%s", runErr, output)
				}
				if !workerReady.Load() || targetFailures.Load() != 1 || mutations.Load() != 0 {
					t.Fatalf("wrong request boundary: worker=%t failures=%d mutations=%d output=%s", workerReady.Load(), targetFailures.Load(), mutations.Load(), output)
				}
				if !strings.Contains(string(output), "summary: copied=0 skipped=0 deleted=0 failed=1 canceled=0 pending=0 bytes=0") {
					t.Fatalf("failure summary missing: %s", output)
				}
				content, err := os.ReadFile(source)
				if err != nil || string(content) != "abcdefgh" {
					t.Fatalf("source lost or changed: %q %v", content, err)
				}
				info, err := os.Stat(source)
				if err != nil || !info.ModTime().Equal(stamp) {
					t.Fatalf("source metadata changed: %v", err)
				}
			})
		}
	}
}
