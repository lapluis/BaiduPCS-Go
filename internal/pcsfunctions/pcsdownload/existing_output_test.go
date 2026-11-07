package pcsdownload

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingDownloadDoesNotHideCheckpointInspectionFailure(t *testing.T) {
	name := filepath.Join(t.TempDir(), strings.Repeat("x", 240))
	content := []byte("existing partial content")
	if err := os.WriteFile(name, content, 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint := name + DownloadSuffix
	_, inspectErr := os.Stat(checkpoint)
	if inspectErr == nil || os.IsNotExist(inspectErr) {
		t.Fatalf("fixture did not produce a checkpoint inspection error: %v", inspectErr)
	}
	if FileExist(name) {
		t.Error("checkpoint inspection error was treated as absent checkpoint")
	}
	task := emptyTask(name, false, false)
	task.FileInfo.Size = 100
	result := task.Run()
	var pathErr *os.PathError
	if result.Succeed || result.NeedRetry || !errors.As(result.Err, &pathErr) || pathErr.Path != checkpoint {
		t.Fatalf("checkpoint inspection failure reported success or lost cause: %+v", result)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != string(content) {
		t.Fatalf("inspection failure changed existing file: %q %v", got, err)
	}
}

func TestExistingDownloadInspectionRequiresRegularOutputAndCheckpoint(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "complete", "partial", "output-directory", "checkpoint-directory"} {
		t.Run(mode, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "output")
			switch mode {
			case "missing":
			case "output-directory":
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				content := []byte("existing data")
				if mode == "empty" {
					content = nil
				}
				if err := os.WriteFile(name, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "partial" {
				if err := os.WriteFile(name+DownloadSuffix, []byte("saved ranges"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "checkpoint-directory" {
				if err := os.Mkdir(name+DownloadSuffix, 0700); err != nil {
					t.Fatal(err)
				}
			}
			exists, err := FileExistWithError(name)
			wantExists := mode == "complete"
			wantError := mode == "output-directory" || mode == "checkpoint-directory"
			if exists != wantExists || (err != nil) != wantError || FileExist(name) != wantExists {
				t.Fatalf("inspection misclassified %s: exists=%v err=%v", mode, exists, err)
			}
			if wantError || wantExists {
				task := emptyTask(name, false, false)
				task.FileInfo.Size = 8
				// No account client: local rejection or skip must precede discovery.
				result := task.Run()
				if result.Succeed != wantExists || result.NeedRetry {
					t.Fatalf("wrong task result: %+v", result)
				}
				if wantError {
					var pathErr *os.PathError
					if !errors.As(result.Err, &pathErr) || pathErr.Path != name && pathErr.Path != name+DownloadSuffix {
						t.Fatalf("local error lost: %v", result.Err)
					}
				} else if result.Err != nil {
					t.Fatalf("healthy skip failed: %v", result.Err)
				}
			}
			if mode == "partial" {
				if got, err := os.ReadFile(name + DownloadSuffix); err != nil || string(got) != "saved ranges" {
					t.Fatalf("inspection changed checkpoint: %q %v", got, err)
				}
			}
			if mode != "missing" && mode != "output-directory" && mode != "empty" {
				if got, err := os.ReadFile(name); err != nil || string(got) != "existing data" {
					t.Fatalf("inspection changed output: %q %v", got, err)
				}
			}
		})
	}
}
