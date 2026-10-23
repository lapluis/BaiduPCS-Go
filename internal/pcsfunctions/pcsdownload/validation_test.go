package pcsdownload

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

func TestDownloadValidationRequiresExistingRegularFile(t *testing.T) {
	for _, skipChecksum := range []bool{false, true} {
		for _, directory := range []bool{false, true} {
			filename := filepath.Join(t.TempDir(), "output")
			var size int64 = 1
			if directory {
				if err := os.Mkdir(filename, 0700); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(filename)
				if err != nil {
					t.Fatal(err)
				}
				size = info.Size()
			}
			unit := emptyTask(filename, false, false)
			unit.FileInfo.Size = size
			unit.NoCheck = skipChecksum
			result := &taskframework.TaskUnitRunResult{}
			if unit.checkFileValid(result) || result.Err == nil {
				t.Errorf("skipChecksum=%v directory=%v: accepted invalid local output: %+v", skipChecksum, directory, result)
			}
		}
	}
}

func TestTestModeDoesNotValidateExistingLocalFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(filename, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	unit := emptyTask(filename, false, true)
	unit.FileInfo.Size = 100
	result := &taskframework.TaskUnitRunResult{}
	if !unit.checkFileValid(result) {
		t.Fatalf("test download validated unrelated output: %+v", result)
	}
}

func TestDownloadLengthMismatchCannotBeSkippedOnRetry(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(filename, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	unit := emptyTask(filename, false, false)
	unit.FileInfo.Size = 10
	result := &taskframework.TaskUnitRunResult{}
	if unit.checkFileValid(result) || !result.NeedRetry || !unit.IsOverwrite {
		t.Fatalf("invalid output can be skipped on retry: overwrite=%v result=%+v", unit.IsOverwrite, result)
	}
}

func TestDownloadValidationAcceptsValidOutput(t *testing.T) {
	for _, mode := range []string{"skip_checksum", "unsupported_checksum", "matching_checksum", "wrong_checksum"} {
		t.Run(mode, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "output")
			content := []byte("valid output")
			if err := os.WriteFile(filename, content, 0600); err != nil {
				t.Fatal(err)
			}
			unit := emptyTask(filename, false, false)
			unit.FileInfo.Size = int64(len(content))
			unit.NoCheck = mode == "skip_checksum"
			if mode == "matching_checksum" || mode == "wrong_checksum" {
				unit.FileInfo.BlockList = []string{"synthetic block"}
				unit.FileInfo.MD5 = fmt.Sprintf("%x", md5.Sum(content))
				if mode == "wrong_checksum" {
					unit.FileInfo.MD5 = "00000000000000000000000000000000"
				}
			}
			result := &taskframework.TaskUnitRunResult{}
			ok := unit.checkFileValid(result)
			if mode == "wrong_checksum" {
				if ok || !result.NeedRetry || !unit.IsOverwrite {
					t.Fatalf("bad checksum was not retried with overwrite: %+v", result)
				}
			} else if !ok {
				t.Fatalf("valid output rejected: %+v", result)
			}
		})
	}
}
