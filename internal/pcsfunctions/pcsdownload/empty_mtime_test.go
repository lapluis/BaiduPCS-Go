package pcsdownload

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/filelock"
)

func TestEmptyDownloadAppliesModificationTimeOnlyToWrittenOutput(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, overwrite := range []bool{false, true} {
			for _, testOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("existing=%v/overwrite=%v/test=%v", existing, overwrite, testOnly), func(t *testing.T) {
					name := filepath.Join(t.TempDir(), "nested", "output")
					originalTime := time.Unix(1600000000, 0)
					if existing {
						if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(name, []byte("keep existing data"), 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Chtimes(name, originalTime, originalTime); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(name+DownloadSuffix, []byte("keep existing ranges"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					task := emptyTask(name, overwrite, testOnly)
					task.ModifyMTime = true
					task.FileInfo.Mtime = 1700000000
					result := task.Run()
					if !result.Succeed || result.Err != nil {
						t.Fatalf("empty download failed: %+v", result)
					}
					if testOnly && !existing {
						if _, err := os.Stat(filepath.Dir(name)); !os.IsNotExist(err) {
							t.Fatalf("test mode created directory: %v", err)
						}
						return
					}
					info, err := os.Stat(name)
					if err != nil {
						t.Fatal(err)
					}
					wantContent, wantTime := "", time.Unix(task.FileInfo.Mtime, 0)
					preserved := existing && (!overwrite || testOnly)
					if preserved {
						wantContent, wantTime = "keep existing data", originalTime
					}
					if got, err := os.ReadFile(name); err != nil || string(got) != wantContent {
						t.Fatalf("wrong output: %q %v", got, err)
					}
					if !info.ModTime().Equal(wantTime) {
						t.Fatalf("wrong modification time: got=%v want=%v", info.ModTime(), wantTime)
					}
					checkpoint, err := os.ReadFile(name + DownloadSuffix)
					if preserved {
						if err != nil || string(checkpoint) != "keep existing ranges" {
							t.Fatalf("skipped output lost ranges: %q %v", checkpoint, err)
						}
					} else if !os.IsNotExist(err) {
						t.Fatalf("written empty output retained stale checkpoint: %q %v", checkpoint, err)
					}
					if err := os.Rename(name, name+".released"); err != nil {
						t.Fatalf("empty output left open: %v", err)
					}
				})
			}
		}
	}
}

func TestEmptyDownloadModificationTimeHonorsActiveOwner(t *testing.T) {
	name := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(name, []byte("active data"), 0600); err != nil {
		t.Fatal(err)
	}
	originalTime := time.Unix(1600000000, 0)
	if err := os.Chtimes(name, originalTime, originalTime); err != nil {
		t.Fatal(err)
	}
	checkpoint := name + DownloadSuffix
	if err := os.WriteFile(checkpoint, []byte("active ranges"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := filelock.TryLock(checkpoint + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	task := emptyTask(name, true, false)
	task.ModifyMTime = true
	task.FileInfo.Mtime = 1700000000
	result := task.Run()
	if result.Succeed || result.Err == nil || result.NeedRetry {
		t.Fatalf("empty task bypassed owner: %+v", result)
	}
	info, err := os.Stat(name)
	if err != nil || !info.ModTime().Equal(originalTime) {
		t.Fatalf("busy task changed modification time: %v %v", info, err)
	}
	for path, want := range map[string]string{name: "active data", checkpoint: "active ranges"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("busy task changed data: %q %v", got, err)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	result = task.Run()
	if !result.Succeed || result.Err != nil {
		t.Fatalf("released task failed: %+v", result)
	}
	info, err = os.Stat(name)
	if err != nil || info.Size() != 0 || !info.ModTime().Equal(time.Unix(task.FileInfo.Mtime, 0)) {
		t.Fatalf("retry did not apply empty output and time: %v %v", info, err)
	}
	if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
		t.Fatalf("retry retained stale checkpoint: %v", err)
	}
}
