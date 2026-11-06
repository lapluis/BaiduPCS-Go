package rc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

type stageReadFixture struct {
	remote *Remote
	target string
	entry  Entry
	stage  downloadStage
	before map[string][]byte
}

func newStageReadFixture(t *testing.T) stageReadFixture {
	t.Helper()
	f := stageReadFixture{remote: &Remote{UID: 7}, target: filepath.Join(t.TempDir(), "result.bin"), entry: Entry{Size: 8, ID: "fixture-file", ModTime: time.Unix(100, 0)}}
	var err error
	f.stage, err = stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.stage.payload, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.stage.payload+pcsdownload.DownloadSuffix, []byte("fixture range state"), 0600); err != nil {
		t.Fatal(err)
	}
	f.stage.info.ETag = `"fixture-entity"`
	if err := f.stage.save(); err != nil {
		t.Fatal(err)
	}
	f.before = make(map[string][]byte)
	for _, path := range []string{f.stage.record, f.stage.payload, f.stage.payload + pcsdownload.DownloadSuffix} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f.before[path] = data
	}
	return f
}

func (f stageReadFixture) checkPreservedAndRetry(t *testing.T) {
	t.Helper()
	for path, want := range f.before {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("resume file changed after inspection failure: %s (%v)", filepath.Base(path), err)
		}
	}
	next, err := stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
	if err != nil || next.info.ETag != f.stage.info.ETag || next.info.LocalSize != int64(len("partial")) {
		t.Errorf("existing progress could not resume on retry: %+v %v", next.info, err)
	}
	if _, err := os.Stat(f.target); !os.IsNotExist(err) {
		t.Errorf("inspection published destination: %v", err)
	}
}

func TestDownloadStageInspectionFailurePreservesProgress(t *testing.T) {
	for _, kind := range []string{"record", "payload", "ranges"} {
		for _, cause := range []error{os.ErrPermission, errors.New("fixture transient I/O failure")} {
			t.Run(kind+"/"+cause.Error(), func(t *testing.T) {
				f := newStageReadFixture(t)
				previousRead, previousStat := readDownloadRecord, statDownloadState
				defer func() { readDownloadRecord, statDownloadState = previousRead, previousStat }()
				if kind == "record" {
					readDownloadRecord = func(path string) ([]byte, error) { return nil, &os.PathError{Op: "read", Path: path, Err: cause} }
				} else {
					failedPath := f.stage.payload
					if kind == "ranges" {
						failedPath += pcsdownload.DownloadSuffix
					}
					statDownloadState = func(path string) (os.FileInfo, error) {
						if path == failedPath {
							return nil, &os.PathError{Op: "stat", Path: path, Err: cause}
						}
						return previousStat(path)
					}
				}
				_, err := stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
				if !errors.Is(err, cause) {
					t.Errorf("inspection error lost: %v", err)
				}
				readDownloadRecord, statDownloadState = previousRead, previousStat
				f.checkPreservedAndRetry(t)
			})
		}
	}
}

func TestDownloadStageMissingFilesStillRestart(t *testing.T) {
	for _, kind := range []string{"record", "payload", "ranges"} {
		t.Run(kind, func(t *testing.T) {
			f := newStageReadFixture(t)
			path := f.stage.record
			if kind == "payload" {
				path = f.stage.payload
			}
			if kind == "ranges" {
				path = f.stage.payload + pcsdownload.DownloadSuffix
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			next, err := stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
			if err != nil || next.info.ETag != "" || next.info.LocalSize != 0 {
				t.Fatalf("missing state was not restarted: %+v %v", next.info, err)
			}
			for _, path := range []string{f.stage.payload, f.stage.payload + pcsdownload.DownloadSuffix} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("invalid partial retained: %v", err)
				}
			}
		})
	}
}

func TestDownloadInspectionFailureStopsTransferAndAllowsRetry(t *testing.T) {
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
			if err := os.WriteFile(dst.Root, []byte("old destination"), 0600); err != nil {
				t.Fatal(err)
			}
			o := opts()
			plan, err := FilePlan(context.Background(), src, dst, move, o)
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
			if err := os.WriteFile(stage.payload+pcsdownload.DownloadSuffix, []byte("range state"), 0600); err != nil {
				t.Fatal(err)
			}
			stage.info.ETag = `"fixture-entity"`
			if err := stage.save(); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, path := range []string{stage.record, stage.payload, stage.payload + pcsdownload.DownloadSuffix} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = data
			}
			previousRead, previousWorker := readDownloadRecord, runTransferWorker
			defer func() { readDownloadRecord, runTransferWorker = previousRead, previousWorker }()
			cause := errors.New("fixture checkpoint inspection failure")
			readDownloadRecord = func(string) ([]byte, error) { return nil, cause }
			calls := 0
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, from, to string, _ int64) error {
				calls++
				if kind != "download" || from != src.Root {
					return errors.New("unexpected worker invocation")
				}
				if calls == 1 {
					settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
					data, err := os.ReadFile(to)
					if err != nil || string(data) != "abc" || settings.ETag != stage.info.ETag {
						t.Error("retry discarded saved progress")
					}
				}
				return os.WriteFile(to, content, 0600)
			}
			result, err := Execute(context.Background(), plan, o)
			if !errors.Is(err, cause) || result.Failed != 1 || result.Copied != 0 || calls != 0 {
				t.Fatalf("inspection failure did not stop transfer: %+v %v calls=%d", result, err, calls)
			}
			for path, want := range before {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, want) {
					t.Errorf("failed inspection changed state: %s %v", filepath.Base(path), err)
				}
			}
			if !bytes.Equal(cloud.files[src.Root], content) {
				t.Fatal("failed inspection removed source")
			}
			if data, err := os.ReadFile(dst.Root); err != nil || string(data) != "old destination" {
				t.Fatal("failed inspection replaced destination")
			}
			readDownloadRecord = previousRead
			result, err = Execute(context.Background(), plan, o)
			if err != nil || result.Copied != 1 {
				t.Fatalf("retry failed: %+v %v", result, err)
			}
			if data, err := os.ReadFile(dst.Root); err != nil || !bytes.Equal(data, content) {
				t.Fatal("retry published incorrect data")
			}
			_, sourceExists := cloud.files[src.Root]
			if sourceExists == move {
				t.Fatalf("wrong source lifetime after successful retry: move=%v sourceExists=%v", move, sourceExists)
			}
		})
	}
}
