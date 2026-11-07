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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestCLIUploadResumesCompletedParts(t *testing.T) {
	cli := newUploadCLIExecutable(t)
	const blockSize = 4 * 1024 * 1024
	original := make([]byte, 2*blockSize+17)
	for i := range original {
		original[i] = byte((i*17 + i/257) % 251)
	}
	for _, tc := range []struct {
		mode   string
		edited bool
	}{{"copyto", false}, {"moveto", false}, {"copyto", true}, {"moveto", true}} {
		mode, edited := tc.mode, tc.edited
		t.Run(fmt.Sprintf("%s/edited=%t", mode, edited), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			if err := os.WriteFile(source, original, 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1700000000, 0)
			if err := os.Chtimes(source, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			var recoverPart, committed atomic.Bool
			var merges, downloads atomic.Int32
			var mu sync.Mutex
			attempts := map[int]int{}
			stored := map[int][]byte{}
			expected := original
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				switch {
				case r.URL.Query().Get("method") == "meta":
					if !committed.Load() {
						io.WriteString(w, `{"error_code":31066}`)
					} else {
						if _, err := os.Stat(source); err != nil {
							t.Error("source removed before resumed destination verification", err)
						}
						fmt.Fprintf(w, `{"error_code":0,"list":[{"fs_id":9,"path":"/target","size":%d}]}`, len(original))
					}
				case r.URL.Path == "/api/list":
					io.WriteString(w, `{"errno":0,"list":[]}`)
				case r.URL.Path == "/api/user/getinfo":
					io.WriteString(w, `{"errno":0,"records":[{"uk":7}]}`)
				case r.URL.Path == "/api/precreate":
					io.WriteString(w, `{"errno":0,"return_type":1,"uploadid":"fixture-session"}`)
				case r.URL.Query().Get("method") == "locateupload":
					io.WriteString(w, `{"error_code":31064}`)
				case r.URL.Path == "/rest/2.0/pcs/superfile2":
					q := r.URL.Query()
					id, err := strconv.Atoi(q.Get("partseq"))
					if err != nil || id < 0 || id > 2 || q.Get("uploadid") != "fixture-session" || q.Get("path") != "/target" || q.Get("partoffset") != fmt.Sprint(id*blockSize) {
						t.Error("invalid resumed part identity", err)
						http.Error(w, "invalid part", 400)
						return
					}
					reader, err := r.MultipartReader()
					if err != nil {
						t.Error(err)
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
					attempts[id]++
					mu.Unlock()
					if id == 2 && !recoverPart.Load() {
						w.WriteHeader(http.StatusInternalServerError)
						io.WriteString(w, `{"error_code":31112}`)
						return
					}
					mu.Lock()
					stored[id] = append([]byte(nil), content...)
					mu.Unlock()
					fmt.Fprintf(w, `{"md5":"%x"}`, md5.Sum(content))
				case r.URL.Path == "/api/create":
					merges.Add(1)
					if _, err := os.Stat(source); err != nil {
						t.Error("source removed before resumed merge", err)
					}
					var blocks []string
					if err := r.ParseForm(); err != nil || r.Form.Get("uploadid") != "fixture-session" || r.Form.Get("path") != "/target" || r.Form.Get("size") != fmt.Sprint(len(original)) {
						t.Error("invalid resumed merge identity", err)
					}
					mu.Lock()
					want := append([]byte(nil), expected...)
					mu.Unlock()
					if err := json.Unmarshal([]byte(r.Form.Get("block_list")), &blocks); err != nil || len(blocks) != 3 {
						t.Error("resumed merge lost part checksums", blocks, err)
					} else {
						for id, digest := range blocks {
							if digest != fmt.Sprintf("%x", md5.Sum(want[id*blockSize:min((id+1)*blockSize, len(want))])) {
								t.Errorf("resumed merge used stale checksum for part %d", id)
							}
						}
					}
					mu.Lock()
					assembled := bytes.Join([][]byte{stored[0], stored[1], stored[2]}, nil)
					mu.Unlock()
					if !bytes.Equal(assembled, want) {
						t.Error("merged content differs from source")
					}
					committed.Store(true)
					io.WriteString(w, `{"errno":0}`)
				case r.URL.Query().Get("method") == "locatedownload":
					io.WriteString(w, `{"error_code":0,"urls":[{"url":"http://download.fixture.invalid/content","encrypt":0}]}`)
				case r.URL.Hostname() == "download.fixture.invalid" && r.URL.Path == "/content":
					downloads.Add(1)
					mu.Lock()
					content := bytes.Join([][]byte{stored[0], stored[1], stored[2]}, nil)
					mu.Unlock()
					serveUploadVerificationContent(t, w, r, content)
				default:
					t.Errorf("unexpected fixture request: %s %s method=%s", r.Method, r.URL.Path, r.URL.Query().Get("method"))
					http.Error(w, "unexpected request", 400)
				}
			})
			proxy := cli.proxy(t, handler)
			output, runErr := cli.run(t, root, mode, source, proxy)
			var exit *exec.ExitError
			if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "summary: copied=0 skipped=0 deleted=0 failed=1 canceled=0 pending=0 bytes=0") {
				t.Fatalf("part failure result changed: %v\n%s", runErr, output)
			}
			mu.Lock()
			first, second, third := attempts[0], attempts[1], attempts[2]
			mu.Unlock()
			if first != 1 || second != 1 || third != 3 || merges.Load() != 0 {
				t.Fatalf("failed attempt counts: first=%d second=%d third=%d merges=%d", first, second, third, merges.Load())
			}
			if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, original) {
				t.Fatal("failed upload lost source", err)
			}
			if info, err := os.Stat(source); err != nil || !info.ModTime().Equal(stamp) {
				t.Fatal("failed upload changed source time", err)
			}
			files, err := filepath.Glob(filepath.Join(root, "rc-state", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal("upload checkpoint not isolated", files, err)
			}
			readState := func() []*uploader.InstanceState {
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				var db struct {
					Records []struct {
						State *uploader.InstanceState `json:"state"`
					} `json:"upload_state"`
				}
				if err := json.Unmarshal(data, &db); err != nil {
					t.Fatal(err)
				}
				var states []*uploader.InstanceState
				for _, record := range db.Records {
					states = append(states, record.State)
				}
				return states
			}
			states := readState()
			if len(states) != 1 || states[0] == nil || states[0].Uploadid != "fixture-session" || len(states[0].BlockList) != 3 {
				t.Fatal("failed upload did not preserve complete resume state")
			}
			byID := map[int]string{}
			for _, block := range states[0].BlockList {
				if block == nil {
					t.Fatal("null saved block")
				}
				if block.ID < 0 || block.ID > 2 || block.Range.Begin != int64(block.ID*blockSize) || block.Range.End != int64(min((block.ID+1)*blockSize, len(original))) {
					t.Fatal("saved block range does not reconstruct source")
				}
				byID[block.ID] = block.CheckSum
			}
			if len(byID) != 3 || byID[0] != fmt.Sprintf("%x", md5.Sum(original[:blockSize])) || byID[1] != fmt.Sprintf("%x", md5.Sum(original[blockSize:2*blockSize])) || byID[2] != "" {
				t.Fatal("completed part was not checkpointed independently")
			}
			if edited {
				changed := append([]byte(nil), original...)
				changed[0] ^= 1
				if err := os.WriteFile(source, changed, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(source, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				expected = changed
				mu.Unlock()
			}
			recoverPart.Store(true)
			output, runErr = cli.run(t, root, mode, source, proxy)
			if runErr != nil || !strings.Contains(string(output), fmt.Sprintf("summary: copied=1 skipped=0 deleted=0 failed=0 canceled=0 pending=0 bytes=%d", len(original))) {
				t.Fatalf("resumed upload failed: %v\n%s", runErr, output)
			}
			mu.Lock()
			first, second, third = attempts[0], attempts[1], attempts[2]
			mu.Unlock()
			wantFirst := 1
			if edited {
				wantFirst = 2
			}
			// A changed whole-file digest invalidates the entire old session.
			if first != wantFirst || second != wantFirst || third != 4 || merges.Load() != 1 || len(readState()) != 0 {
				t.Fatalf("resume reused changed data, resent unchanged data or left stale state: first=%d second=%d third=%d merges=%d", first, second, third, merges.Load())
			}
			if mode == "moveto" {
				if _, err := os.Stat(source); !os.IsNotExist(err) || downloads.Load() == 0 {
					t.Fatal("resumed move did not verify content before removal", err)
				}
			} else {
				mu.Lock()
				want := append([]byte(nil), expected...)
				mu.Unlock()
				if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, want) {
					t.Fatal("resumed copy changed source", err)
				}
				if info, err := os.Stat(source); err != nil || !info.ModTime().Equal(stamp) {
					t.Fatal("resumed copy changed source time", err)
				}
			}
		})
	}
}
