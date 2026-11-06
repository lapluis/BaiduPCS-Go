package rc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

func TestDownloadSharedPartialRetainsBackupAndResumes(t *testing.T) {
	for _, move := range []bool{false, true} {
		name := "copy"
		if move {
			name = "move"
		}
		t.Run(name, func(t *testing.T) {
			content := []byte("abcdefgh")
			cloud := &fakeCloud{files: map[string][]byte{"/Temp/file": content}, id: 101}
			src := cloud.endpoint(7, "/Temp/file")
			dst := Endpoint{FS: &Local{}, Root: filepath.Join(t.TempDir(), "result.bin")}
			plan, err := FilePlan(context.Background(), src, dst, move, opts())
			if err != nil {
				t.Fatal(err)
			}
			stage, err := stageFor(context.Background(), src.FS.(*Remote), src.Root, dst.Root, *plan.Actions[0].Source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stage.payload, []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stage.payload+pcsdownload.DownloadSuffix, []byte("fixture range state"), 0600); err != nil {
				t.Fatal(err)
			}
			stage.info.ETag = `"fixture-entity"`
			if err := stage.save(); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(t.TempDir(), "partial-backup.bin")
			if err := os.Link(stage.payload, backup); err != nil {
				t.Fatal(err)
			}
			backupInfo, err := os.Stat(backup)
			if err != nil {
				t.Fatal(err)
			}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			calls := 0
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, from, to string, _ int64) error {
				calls++
				if kind != "download" || from != src.Root {
					t.Errorf("unexpected worker: %s %s", kind, from)
				}
				if calls > 1 {
					return os.WriteFile(to, content, 0600)
				} // Move verification hashes the source.
				settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
				data, err := os.ReadFile(to)
				if err != nil || string(data) != "abc" || settings.ETag != stage.info.ETag {
					t.Error("saved partial or validator discarded")
				}
				info, err := os.Stat(to)
				if err != nil {
					return err
				}
				if os.SameFile(info, backupInfo) {
					t.Error("worker received the externally linked payload")
				}
				file, err := os.OpenFile(to, os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				_, writeErr := file.WriteAt(content[3:], 3)
				closeErr := file.Close()
				if writeErr != nil {
					return writeErr
				}
				return closeErr
			}
			result, err := Execute(context.Background(), plan, opts())
			if err != nil || result.Copied != 1 {
				t.Fatalf("resume failed: %+v %v", result, err)
			}
			if data, err := os.ReadFile(dst.Root); err != nil || !bytes.Equal(data, content) {
				t.Errorf("wrong result: %q %v", data, err)
			}
			if data, err := os.ReadFile(backup); err != nil || string(data) != "abc" {
				t.Errorf("external backup overwritten: %q %v", data, err)
			}
			if info, err := os.Stat(backup); err != nil || !info.ModTime().Equal(backupInfo.ModTime()) {
				t.Errorf("external backup timestamp changed: %v", err)
			}
			_, sourceExists := cloud.files[src.Root]
			if sourceExists == move {
				t.Errorf("wrong source lifetime: move=%v exists=%v", move, sourceExists)
			}
		})
	}
}

func TestDownloadSharedPartialPreparationFailuresRetainProgress(t *testing.T) {
	for _, mode := range []string{"write", "sync", "close", "write-close", "sync-close", "cancel", "changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newStageReadFixture(t)
			backup := filepath.Join(t.TempDir(), "backup.bin")
			if err := os.Link(f.stage.payload, backup); err != nil {
				t.Fatal(err)
			}
			backupInfo, err := os.Stat(backup)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reports := 0
			ctx = context.WithValue(ctx, progressKey{}, func(transferProgress) { reports++ })
			failure := errors.New("fixture shared partial " + mode + " failure")
			closeFailure := errors.New("fixture secondary close failure")
			previous := openLocalCopyOutput
			defer func() { openLocalCopyOutput = previous }()
			var output *faultLocalCopyOutput
			openLocalCopyOutput = func(path string) (localCopyOutput, error) {
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
				if err != nil {
					return nil, err
				}
				output = &faultLocalCopyOutput{File: file}
				switch mode {
				case "write", "write-close":
					output.writeErr = failure
				case "sync", "sync-close":
					output.syncErr = failure
				case "close":
					output.closeErr = failure
				case "cancel":
					output.afterWrite = cancel
				case "changed":
					output.afterWrite = func() {
						if err := os.WriteFile(backup, []byte("external edit"), 0600); err != nil {
							t.Error(err)
						}
					}
				}
				if strings.HasSuffix(mode, "-close") {
					output.closeErr = closeFailure
				}
				return output, nil
			}
			_, err = stageFor(ctx, f.remote, "/file", f.target, f.entry)
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation lost: %v", err)
				}
			} else if mode == "changed" {
				if err == nil || !strings.Contains(err.Error(), "partial download changed") {
					t.Errorf("concurrent edit accepted: %v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Errorf("preparation error lost: %v", err)
			}
			if strings.HasSuffix(mode, "-close") && !errors.Is(err, closeFailure) {
				t.Errorf("close error lost: %v", err)
			}
			if output == nil || output.closes != 1 {
				t.Fatalf("clone handle leaked: %+v", output)
			}
			if reports != 0 {
				t.Errorf("local preparation counted as network transfer: reports=%d", reports)
			}
			current, err := os.Stat(f.stage.payload)
			if err != nil || !os.SameFile(current, backupInfo) {
				t.Error("failed preparation replaced original payload")
			}
			entries, err := os.ReadDir(filepath.Dir(f.stage.payload))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".detach-") {
					t.Errorf("temporary clone leaked: %s", entry.Name())
				}
			}
			openLocalCopyOutput = previous
			if mode == "changed" {
				for _, path := range []string{f.stage.record, f.stage.payload + pcsdownload.DownloadSuffix} {
					if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, f.before[path]) {
						t.Error("concurrent edit rewrote checkpoint")
					}
				}
				if data, err := os.ReadFile(backup); err != nil || string(data) != "external edit" {
					t.Error("external edit was overwritten")
				}
				return
			}
			f.checkPreservedAndRetry(t)
			if data, err := os.ReadFile(backup); err != nil || string(data) != "partial" {
				t.Error("retry changed external backup")
			}
			current, err = os.Stat(f.stage.payload)
			if err != nil || os.SameFile(current, backupInfo) {
				t.Error("retry retained shared payload")
			}
		})
	}
}

func TestDownloadSingleLinkPartialAvoidsCopy(t *testing.T) {
	f := newStageReadFixture(t)
	before, err := os.Stat(f.stage.payload)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, before) {
		t.Fatal("cannot capture original identity")
	}
	previous := openLocalCopyOutput
	defer func() { openLocalCopyOutput = previous }()
	openLocalCopyOutput = func(string) (localCopyOutput, error) {
		t.Error("ordinary partial copied unnecessarily")
		return nil, errors.New("unexpected clone")
	}
	f.checkPreservedAndRetry(t)
	after, err := os.Stat(f.stage.payload)
	if err != nil || !os.SameFile(before, after) {
		t.Error("ordinary partial identity changed")
	}
}
