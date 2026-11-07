package pcsdownload

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

// Model cancellation immediately after a caller observes an active context.
type cancelAfterValidationCheck struct {
	context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelAfterValidationCheck) Err() error {
	err := c.Context.Err()
	c.once.Do(c.cancel)
	return err
}

func TestDownloadValidationHonorsTaskCancellation(t *testing.T) {
	for _, mode := range []string{"checksum", "skip-checksum", "unsupported-checksum", "test-mode", "between-entry-and-hash", "between-entry-and-skip", "between-entry-and-unsupported", "between-entry-and-test"} {
		for _, deadline := range []bool{false, true} {
			late := strings.HasPrefix(mode, "between-entry")
			if late && deadline {
				continue
			}
			t.Run(fmt.Sprintf("%s/deadline=%v", mode, deadline), func(t *testing.T) {
				name := filepath.Join(t.TempDir(), "output")
				content := []byte("synthetic completed download")
				checkpoint := []byte("retain checkpoint")
				if err := os.WriteFile(name, content, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name+DownloadSuffix, checkpoint, 0600); err != nil {
					t.Fatal(err)
				}
				task := emptyTask(name, false, mode == "test-mode" || mode == "between-entry-and-test")
				task.FileInfo.Size = int64(len(content))
				task.FileInfo.BlockList = []string{"synthetic block"}
				task.FileInfo.MD5 = fmt.Sprintf("%x", md5.Sum(content))
				task.NoCheck = mode == "skip-checksum" || mode == "between-entry-and-skip"
				if mode == "unsupported-checksum" || mode == "between-entry-and-unsupported" {
					task.FileInfo.BlockList = nil
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var want error = context.Canceled
				if deadline {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer cancel()
					want = context.DeadlineExceeded
				} else if !late {
					cancel()
				}
				task.Context = ctx
				if late {
					task.Context = &cancelAfterValidationCheck{Context: ctx, cancel: cancel}
				}
				result := &taskframework.TaskUnitRunResult{}
				if task.checkFileValid(result) || !errors.Is(result.Err, want) || result.NeedRetry || result.NeedNextdindex || task.IsOverwrite {
					t.Fatalf("canceled validation accepted or requested retry: result=%+v overwrite=%v", result, task.IsOverwrite)
				}
				for path, original := range map[string][]byte{name: content, name + DownloadSuffix: checkpoint} {
					if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
						t.Fatalf("canceled validation changed %s: %q %v", path, got, err)
					}
				}
			})
		}
	}
}

// Cancel after several active-context observations so hashing must keep
// polling the supplied context across its read batches, without timer races.
type cancelDuringValidation struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cancelDuringValidation) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestDownloadValidationCancellationWhileHashing(t *testing.T) {
	for _, throughTask := range []bool{false, true} {
		t.Run(fmt.Sprint(throughTask), func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "output")
			content := bytes.Repeat([]byte("x"), 4*checksum.DefaultBufSize)
			if err := os.WriteFile(name, content, 0600); err != nil {
				t.Fatal(err)
			}
			task := emptyTask(name, false, false)
			task.FileInfo.Size = int64(len(content))
			task.FileInfo.BlockList = []string{"synthetic block"}
			task.FileInfo.MD5 = fmt.Sprintf("%x", md5.Sum(content))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			polling := &cancelDuringValidation{Context: ctx, cancel: cancel, remaining: 8}
			var err error
			if throughTask {
				task.Context = polling
				result := &taskframework.TaskUnitRunResult{}
				if task.checkFileValid(result) || result.NeedRetry || result.NeedNextdindex || task.IsOverwrite {
					t.Fatalf("interrupted hash accepted or retried: %+v", result)
				}
				err = result.Err
			} else {
				err = CheckFileValidWithContext(polling, name, task.FileInfo)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("hash ignored supplied context: %v", err)
			}
			if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, content) {
				t.Fatal("canceled checksum changed payload", err)
			}
			if err := os.Rename(name, name+".released"); err != nil {
				t.Fatalf("canceled checksum retained file handle: %v", err)
			}
		})
	}
}
