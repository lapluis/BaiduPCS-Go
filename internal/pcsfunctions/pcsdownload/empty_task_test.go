package pcsdownload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func emptyTask(path string, overwrite, testOnly bool) *DownloadTaskUnit {
	return &DownloadTaskUnit{
		taskInfo: &taskframework.TaskInfo{}, Cfg: &downloader.Config{IsTest: testOnly},
		SavePath: path, PcsPath: "/empty", IsOverwrite: overwrite,
		FileInfo: &baidupcs.FileDirectory{Path: "/empty", Filename: "empty", Size: 0},
	}
}

func TestEmptyTaskRespectsOverwritePolicy(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		for _, resume := range []bool{false, true} {
			filename := filepath.Join(t.TempDir(), "data")
			if err := os.WriteFile(filename, []byte("only copy"), 0600); err != nil {
				t.Fatal(err)
			}
			if resume {
				if err := os.WriteFile(filename+DownloadSuffix, []byte("synthetic state"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result := emptyTask(filename, overwrite, false).Run()
			if !result.Succeed || result.Err != nil {
				t.Fatalf("empty task failed: %+v", result)
			}
			got, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			want := "only copy"
			if overwrite {
				want = ""
			}
			if string(got) != want {
				t.Errorf("overwrite=%v, resume=%v: got %q, want %q", overwrite, resume, got, want)
			}
		}
	}
}

func TestEmptyTaskCreatesParentsAndClosesFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "nested", "data")
	result := emptyTask(filename, false, false).Run()
	if !result.Succeed || result.Err != nil {
		t.Fatalf("empty task failed: %+v", result)
	}
	info, err := os.Stat(filename)
	if err != nil || info.Size() != 0 {
		t.Fatalf("empty output: %v, %v", info, err)
	}
	if err := os.Rename(filename, filename+".moved"); err != nil {
		t.Fatalf("output file left open: %v", err)
	}
}

func TestEmptyTaskReportsLocalCreationErrors(t *testing.T) {
	for _, targetIsDirectory := range []bool{false, true} {
		dir := t.TempDir()
		filename := dir
		if !targetIsDirectory {
			parent := filepath.Join(dir, "file-parent")
			if err := os.WriteFile(parent, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			filename = filepath.Join(parent, "data")
		}
		result := emptyTask(filename, false, false).Run()
		if result.Succeed || result.Err == nil {
			t.Fatalf("creation failure reported as success: %+v", result)
		}
	}
}

func TestEmptyTaskTestModeDoesNotModifyFiles(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "existing")
	if err := os.WriteFile(filename, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filename, filepath.Join(dir, "missing", "data")} {
		result := emptyTask(name, true, true).Run()
		if !result.Succeed || result.Err != nil {
			t.Fatalf("test-mode task failed: %+v", result)
		}
	}
	got, err := os.ReadFile(filename)
	if err != nil || string(got) != "preserve" {
		t.Fatalf("test mode modified existing data: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("test mode created output parent: %v", err)
	}
}

func TestDirectoryTaskReportsCreationFailure(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(filename, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	unit := emptyTask(filename, false, false)
	unit.FileInfo.Isdir = true
	result := unit.Run()
	if result.Succeed || result.Err == nil {
		t.Fatalf("directory creation failure reported as success: %+v", result)
	}
	got, err := os.ReadFile(filename)
	if err != nil || string(got) != "preserve" {
		t.Fatalf("directory task changed existing file: %q, %v", got, err)
	}
}
