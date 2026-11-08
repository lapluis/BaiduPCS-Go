package pcscommand

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type exportReuseBody struct {
	io.ReadCloser
	reuse func()
}

func (b exportReuseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.reuse()
	return n, err
}

func TestExportUsesInitialOptionsDuringResponse(t *testing.T) {
	for _, stdout := range []bool{false, true} {
		for _, link := range []bool{false, true} {
			for _, phase := range []string{"headers", "body"} {
				t.Run(fmt.Sprintf("stdout=%t/link=%t/%s", stdout, link, phase), func(t *testing.T) {
					calls := exportFixture(t)
					target := filepath.Join(t.TempDir(), "selected")
					reused := filepath.Join(t.TempDir(), "reused")
					const prefix = "retain-existing-export\n"
					for _, file := range []string{target, reused} {
						if err := os.WriteFile(file, []byte(prefix), 0600); err != nil {
							t.Fatal(err)
						}
					}
					options := &ExportOptions{SavePath: target, RootPath: "/selected", StdOut: stdout, LinkFormat: link}
					replacement := ExportOptions{SavePath: reused, RootPath: "/reused", StdOut: !stdout, LinkFormat: !link}
					reuse := sync.OnceFunc(func() { *options = replacement })
					client := GetBaiduPCS().GetClient()
					transport := client.Client.Transport
					client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
						response, err := transport.RoundTrip(req)
						if err != nil {
							return nil, err
						}
						if phase == "headers" {
							reuse()
						} else {
							response.Body = exportReuseBody{response.Body, reuse}
						}
						return response, nil
					})
					output := captureExport(t, options)
					line := fmt.Sprintf("BaiduPCS-Go rapidupload -length=4 -md5=%s -slicemd5=%s -crc32=0 \"/selected/fixture.txt\"\n", exportFixtureMD5, exportFixtureMD5)
					if link {
						line = exportFixtureMD5 + "#" + exportFixtureMD5 + "#4#fixture.txt\n"
					}
					want := prefix
					if !stdout {
						want += line
					}
					if data, err := os.ReadFile(target); err != nil || string(data) != want {
						t.Errorf("selected output changed mode or format: data=%q want=%q err=%v", data, want, err)
					}
					if data, err := os.ReadFile(reused); err != nil || string(data) != prefix {
						t.Errorf("reused output path changed: data=%q err=%v", data, err)
					}
					if stdout && (!strings.Contains(output, line) || !strings.Contains(output, "导出完毕")) || !stdout && !strings.Contains(output, "导出成功") || strings.Contains(output, "写入文件失败") || *calls != 1 {
						t.Errorf("export followed reused options: calls=%d output=%q", *calls, output)
					}
					if *options != replacement {
						t.Fatal("export overwrote caller's reused options")
					}
				})
			}
		}
	}
}

func TestExportFileModePublishesDefaultFilename(t *testing.T) {
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
	options := &ExportOptions{RootPath: "/selected"}
	output := captureExport(t, options)
	if options.SavePath == "" || filepath.Base(options.SavePath) != options.SavePath {
		t.Fatalf("default output filename was not published: %q", options.SavePath)
	}
	data, err := os.ReadFile(filepath.Join(workdir, options.SavePath))
	if err != nil || !strings.Contains(string(data), `"/selected/fixture.txt"`) || !strings.Contains(string(data), exportFixtureMD5) || *calls != 1 || !strings.Contains(output, "导出成功") {
		t.Fatalf("default file export failed: data=%q calls=%d output=%q err=%v", data, *calls, output, err)
	}
}
