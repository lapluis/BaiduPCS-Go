package pcsdownload

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

func TestDownloadChecksumComparesDigestBytesAcrossHexCase(t *testing.T) {
	content := []byte("synthetic completed data with case independent checksum")
	lower := fmt.Sprintf("%x", md5.Sum(content))
	upper := strings.ToUpper(lower)
	mixed := strings.ToUpper(lower[:16]) + lower[16:]
	if lower == upper || mixed == lower || mixed == upper {
		t.Fatal("fixture does not exercise hex letter case")
	}
	for _, tc := range []struct {
		name, digest string
		valid        bool
	}{
		{"lower", lower, true},
		{"upper", upper, true},
		{"mixed", mixed, true},
		{"wrong-lower", strings.Repeat("a", 32), false},
		{"wrong-upper", strings.Repeat("A", 32), false},
		{"short", lower[:30], false},
		{"long", strings.Repeat(lower, 64), false},
		{"non-hex", strings.Repeat("z", 32), false},
		{"whitespace", " " + lower + " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(name, content, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name+DownloadSuffix, []byte("keep ranges"), 0600); err != nil {
				t.Fatal(err)
			}
			task := emptyTask(name, false, false)
			task.FileInfo.Size = int64(len(content))
			task.FileInfo.BlockList = []string{lower}
			task.FileInfo.MD5 = tc.digest
			for _, check := range []func() error{
				func() error { return CheckFileValid(name, task.FileInfo) },
				func() error { return CheckFileValidWithContext(context.Background(), name, task.FileInfo) },
			} {
				err := check()
				if (err == nil) != tc.valid || !tc.valid && !errors.Is(err, ErrDownloadChecksumFailed) {
					t.Fatalf("wrong checksum classification: digest=%s err=%v", tc.digest, err)
				}
			}
			result := &taskframework.TaskUnitRunResult{}
			ok := task.checkFileValid(result)
			if ok != tc.valid || result.NeedRetry != !tc.valid || task.IsOverwrite != !tc.valid || !tc.valid && !errors.Is(result.Err, ErrDownloadChecksumFailed) {
				t.Fatalf("digest case caused incorrect re-download policy: ok=%v overwrite=%v result=%+v", ok, task.IsOverwrite, result)
			}
			for path, want := range map[string]string{name: string(content), name + DownloadSuffix: "keep ranges"} {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Fatalf("validation changed data: %q %v", got, err)
				}
			}
			if err := os.Rename(name, name+".released"); err != nil {
				t.Fatalf("validation left file open: %v", err)
			}
		})
	}
}
