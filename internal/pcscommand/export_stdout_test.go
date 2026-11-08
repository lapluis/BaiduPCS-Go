package pcscommand

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

const exportFixtureMD5 = "e2fc714c4727ee9395f324cd2e7f331f"

func exportFixture(t *testing.T) *int {
	t.Helper()
	previous := pcsconfig.Config
	pcsconfig.Config = pcsconfig.NewConfig(filepath.Join(t.TempDir(), "synthetic.json"))
	pcsconfig.Config.InitDefaultConfig()
	t.Cleanup(func() { pcsconfig.Config = previous })
	calls := new(int)
	GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
		*calls++
		if req.Body != nil {
			defer req.Body.Close()
		}
		if req.URL.Query().Get("method") != "meta" {
			t.Fatalf("unexpected export request: %s", req.URL)
		}
		payload := `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture.txt","size":4,"md5":"` + exportFixtureMD5 + `","block_list":["` + exportFixtureMD5 + `"]}]}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	})
	return calls
}

func captureExport(t *testing.T, options *ExportOptions) string {
	t.Helper()
	return captureExportCall(t, func() { RunExport([]string{"/fixture.txt"}, options) })
}

func captureExportCall(t *testing.T, run func()) string {
	t.Helper()
	capturePath := filepath.Join(t.TempDir(), "stdout")
	capture, err := os.Create(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = capture
	defer func() { os.Stdout = previous; _ = capture.Close() }()
	run()
	os.Stdout = previous
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExportStdoutPreservesOutputPath(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "missing", "invalid-parent"} {
		for _, link := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/link=%t", kind, link), func(t *testing.T) {
				calls := exportFixture(t)
				parent := t.TempDir()
				target := filepath.Join(parent, "output")
				const original = "sole-copy-content"
				switch kind {
				case "file", "invalid-parent":
					if err := os.WriteFile(target, []byte(original), 0600); err != nil {
						t.Fatal(err)
					}
				case "empty-directory":
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Chtimes(parent, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(parent)
				if err != nil {
					t.Fatal(err)
				}
				savePath := target
				if kind == "invalid-parent" {
					savePath = filepath.Join(target, "child")
				}
				output := captureExport(t, &ExportOptions{StdOut: true, SavePath: savePath, LinkFormat: link})
				want := "BaiduPCS-Go rapidupload -length=4"
				if link {
					want = exportFixtureMD5 + "#" + exportFixtureMD5 + "#4#fixture.txt"
				}
				if !strings.Contains(output, want) || !strings.Contains(output, "导出完毕") || *calls != 1 {
					t.Fatalf("stdout export did not complete: calls=%d output=%q", *calls, output)
				}
				if kind == "file" || kind == "invalid-parent" {
					if data, err := os.ReadFile(target); err != nil || string(data) != original {
						t.Errorf("stdout export removed or changed existing file: data=%q err=%v", data, err)
					}
				} else if kind == "empty-directory" {
					if info, err := os.Stat(target); err != nil || !info.IsDir() {
						t.Errorf("stdout export removed existing directory: %v", err)
					}
				} else if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Errorf("stdout export created an output file: %v", err)
				}
				after, err := os.Stat(parent)
				if err != nil || !after.ModTime().Equal(before.ModTime()) {
					t.Errorf("stdout export changed output directory metadata: before=%v after=%v err=%v", before.ModTime(), after, err)
				}
			})
		}
	}
}

func TestExportStdoutDoesNotCreateDefaultOutput(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	}()
	calls := exportFixture(t)
	if err := os.Chtimes(workdir, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(workdir)
	if err != nil {
		t.Fatal(err)
	}
	options := &ExportOptions{StdOut: true}
	output := captureExport(t, options)
	entries, err := os.ReadDir(workdir)
	if err != nil || len(entries) != 0 || options.SavePath != "" {
		t.Fatalf("stdout mode created a default output: entries=%v savePath=%q err=%v", entries, options.SavePath, err)
	}
	after, err := os.Stat(workdir)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("stdout mode touched the working directory", err)
	}
	if *calls != 1 || !strings.Contains(output, "BaiduPCS-Go rapidupload -length=4") || !strings.Contains(output, "导出完毕") {
		t.Fatalf("default stdout export failed: calls=%d output=%q", *calls, output)
	}
}

func TestExportFileModeRetainsAppendBehavior(t *testing.T) {
	for _, kind := range []string{"existing", "missing", "directory"} {
		for _, link := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/link=%t", kind, link), func(t *testing.T) {
				calls := exportFixture(t)
				target := filepath.Join(t.TempDir(), "output")
				prefix := ""
				if kind == "existing" {
					prefix = "retain-existing-export\n"
					if err := os.WriteFile(target, []byte(prefix), 0600); err != nil {
						t.Fatal(err)
					}
				} else if kind == "directory" {
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				}
				output := captureExport(t, &ExportOptions{SavePath: target, LinkFormat: link})
				if kind == "directory" {
					info, err := os.Stat(target)
					if err != nil || !info.IsDir() || *calls != 0 || strings.Contains(output, "导出成功") || !strings.Contains(output, target) {
						t.Fatalf("file-open failure not retained: calls=%d output=%q err=%v", *calls, output, err)
					}
					return
				}
				want := fmt.Sprintf("BaiduPCS-Go rapidupload -length=4 -md5=%s -slicemd5=%s -crc32=0 \"/fixture.txt\"\n", exportFixtureMD5, exportFixtureMD5)
				if link {
					want = exportFixtureMD5 + "#" + exportFixtureMD5 + "#4#fixture.txt\n"
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != prefix+want || *calls != 1 || !strings.Contains(output, "导出成功") {
					t.Fatalf("file export behavior changed: data=%q calls=%d output=%q err=%v", data, *calls, output, err)
				}
			})
		}
	}
}
