package cliintegration

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCLIUploadMergeAndVerification(t *testing.T) {
	cli := newUploadCLIExecutable(t)
	for _, outcome := range []string{"healthy", "merge-error", "verify-mismatch", "verify-content-mismatch"} {
		for _, mode := range []string{"copyto", "moveto"} {
			if outcome == "verify-content-mismatch" && mode != "moveto" {
				continue // Default moves verify content before deleting the source.
			}
			t.Run(outcome+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				source := filepath.Join(root, "source")
				original := []byte("abcdefgh")
				if err := os.WriteFile(source, original, 0600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(1700000000, 0)
				if err := os.Chtimes(source, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				digest := fmt.Sprintf("%x", md5.Sum(original))
				var precreates, parts, merges, verifications, downloads atomic.Int32
				var committed atomic.Bool
				var mu sync.Mutex
				var received []byte
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer r.Body.Close()
					switch {
					case r.URL.Query().Get("method") == "meta":
						if !committed.Load() {
							io.WriteString(w, `{"error_code":31066}`)
							return
						}
						verifications.Add(1)
						if _, err := os.Stat(source); err != nil {
							t.Error("source removed before destination verification", err)
						}
						size := len(original)
						if outcome == "verify-mismatch" {
							size++
						}
						fmt.Fprintf(w, `{"error_code":0,"list":[{"fs_id":9,"path":"/target","size":%d,"isdir":0}]}`, size)
					case r.URL.Path == "/api/list":
						io.WriteString(w, `{"errno":0,"list":[]}`)
					case r.URL.Path == "/api/user/getinfo":
						io.WriteString(w, `{"errno":0,"records":[{"uk":7}]}`)
					case r.URL.Path == "/api/precreate":
						precreates.Add(1)
						if err := r.ParseForm(); err != nil || r.Form.Get("path") != "/target" || r.Form.Get("size") != "8" {
							t.Error("invalid precreation request", err)
						}
						io.WriteString(w, `{"errno":0,"return_type":1,"uploadid":"fixture-session"}`)
					case r.URL.Query().Get("method") == "locateupload":
						// Discovery failure must retain the original, usable PCS host.
						io.WriteString(w, `{"error_code":31064}`)
					case r.URL.Query().Get("method") == "locatedownload":
						io.WriteString(w, `{"error_code":0,"urls":[{"url":"http://download.fixture.invalid/content","encrypt":0}]}`)
					case r.URL.Hostname() == "download.fixture.invalid" && r.URL.Path == "/content":
						downloads.Add(1)
						mu.Lock()
						content := append([]byte(nil), received...)
						mu.Unlock()
						if outcome == "verify-content-mismatch" && len(content) != 0 {
							content[0] ^= 1
						}
						serveUploadVerificationContent(t, w, r, content)
					case r.URL.Path == "/rest/2.0/pcs/superfile2":
						parts.Add(1)
						q := r.URL.Query()
						if q.Get("uploadid") != "fixture-session" || q.Get("path") != "/target" || q.Get("partseq") != "0" || q.Get("partoffset") != "0" {
							t.Error("invalid part identity")
						}
						reader, err := r.MultipartReader()
						if err != nil {
							t.Error(err)
							http.Error(w, "invalid multipart", 400)
							return
						}
						var content []byte
						for {
							part, err := reader.NextPart()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Error(err)
								return
							}
							data, err := io.ReadAll(part)
							part.Close()
							if err != nil {
								t.Error(err)
							}
							if part.FormName() == "uploadedfile" {
								content = append(content, data...)
							}
						}
						mu.Lock()
						received = append([]byte(nil), content...)
						mu.Unlock()
						fmt.Fprintf(w, `{"md5":"%x"}`, md5.Sum(content))
					case r.URL.Path == "/api/create":
						merges.Add(1)
						if _, err := os.Stat(source); err != nil {
							t.Error("source removed before merge", err)
						}
						var blocks []string
						if err := r.ParseForm(); err != nil || r.Form.Get("path") != "/target" || r.Form.Get("size") != "8" || r.Form.Get("uploadid") != "fixture-session" {
							t.Error("invalid merge identity", err)
						}
						if err := json.Unmarshal([]byte(r.Form.Get("block_list")), &blocks); err != nil || len(blocks) != 1 || blocks[0] != digest {
							t.Error("merge did not use uploaded part digest", blocks, err)
						}
						if outcome == "merge-error" {
							io.WriteString(w, `{"errno":31112}`)
							return
						}
						committed.Store(true)
						io.WriteString(w, `{"errno":0}`)
					default:
						t.Errorf("unexpected fixture request: %s %s method=%s", r.Method, r.URL.Path, r.URL.Query().Get("method"))
						http.Error(w, "unexpected request", 400)
					}
				})
				proxy := cli.proxy(t, handler)
				output, runErr := cli.run(t, root, mode, source, proxy)
				if outcome == "healthy" {
					if runErr != nil || !strings.Contains(string(output), "summary: copied=1 skipped=0 deleted=0 failed=0 canceled=0 pending=0 bytes=8") {
						t.Fatalf("healthy upload failed: %v\n%s", runErr, output)
					}
				} else {
					var exit *exec.ExitError
					if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "summary: copied=0 skipped=0 deleted=0 failed=1 canceled=0 pending=0 bytes=0") {
						t.Fatalf("failed upload reported success: %v\n%s", runErr, output)
					}
				}
				if outcome == "verify-mismatch" && !strings.Contains(string(output), "post-transfer size mismatch") {
					t.Fatalf("failure did not reach size verification: %s", output)
				}
				if outcome == "verify-content-mismatch" && !strings.Contains(string(output), "content verification failed; source retained") {
					t.Fatalf("failure did not reach content verification: %s", output)
				}
				mu.Lock()
				contentMatches := bytes.Equal(received, original)
				mu.Unlock()
				if precreates.Load() != 1 || parts.Load() != 1 || merges.Load() != 1 || !contentMatches || (outcome != "merge-error" && verifications.Load() == 0) {
					t.Fatalf("incomplete upload chain: precreate=%d parts=%d merge=%d verify=%d content=%t\n%s", precreates.Load(), parts.Load(), merges.Load(), verifications.Load(), contentMatches, output)
				}
				if mode == "moveto" && (outcome == "healthy" || outcome == "verify-content-mismatch") && downloads.Load() == 0 {
					t.Fatal("move did not verify remote content")
				}
				if mode == "moveto" && outcome == "healthy" {
					if _, err := os.Stat(source); !os.IsNotExist(err) {
						t.Fatal("verified move did not remove source", err)
					}
				} else {
					data, err := os.ReadFile(source)
					if err != nil || !bytes.Equal(data, original) {
						t.Fatalf("source lost or changed: %q %v", data, err)
					}
					info, err := os.Stat(source)
					if err != nil || !info.ModTime().Equal(stamp) {
						t.Fatal("source modification time changed", err)
					}
				}
			})
		}
	}
}
