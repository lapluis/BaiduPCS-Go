package rc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

func TestCancelStopsQueueWithoutReportingFailures(t *testing.T) {
	cloud := &fakeCloud{files: map[string][]byte{}, id: 101}
	for i := 0; i < 6; i++ {
		cloud.files[fmt.Sprintf("/Temp/%d", i)] = []byte("fixture")
	}
	src := cloud.endpoint(1, "/Temp")
	dst := Endpoint{&Local{}, t.TempDir()}
	p := &Plan{Source: src, Destination: dst}
	for i := 0; i < 6; i++ {
		name := fmt.Sprint(i)
		entry, err := src.FS.Stat(context.Background(), src.At(name))
		if err != nil {
			t.Fatal(err)
		}
		p.Actions = append(p.Actions, Action{Kind: "copy", Path: name, Source: &entry})
	}
	previous := runTransferWorker
	defer func() { runTransferWorker = previous }()
	started := make(chan struct{}, 6)
	var calls atomic.Int32
	runTransferWorker = func(ctx context.Context, _ string, _ uint64, _, _ string, _ int64) error {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var result Result
	var runErr error
	var snapshots []ProgressSnapshot
	done := make(chan struct{})
	go func() {
		result, runErr = Execute(ctx, p, Options{Transfers: 2, ShowProgress: true, Progress: func(p ProgressSnapshot) {
			snapshots = append(snapshots, p)
		}, Emit: func(kind, item string) {
			if kind == "error" {
				t.Errorf("cancellation logged as failure: %s", item)
			}
		}})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not join workers")
	}
	if !errors.Is(runErr, context.Canceled) || result.Failed != 0 || result.Canceled != 2 || result.Pending != 4 || calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, runErr, calls.Load())
	}
	if len(snapshots) == 0 {
		t.Fatal("missing final cancellation progress")
	}
	last := snapshots[len(snapshots)-1]
	if !last.Final || last.Active != 0 || last.Canceled != 2 || last.Pending != 4 || last.Failed != 0 || len(last.Files) != 0 {
		t.Fatalf("incorrect final cancellation progress: %+v", last)
	}
}

func TestDownloadStageResumesAndPublishesAtomically(t *testing.T) {
	cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("abcdefgh")}, id: 101}
	src := cloud.endpoint(7, "/Temp/a")
	dst := Endpoint{&Local{}, filepath.Join(t.TempDir(), "result.bin")}
	entry, err := src.FS.Stat(context.Background(), src.Root)
	if err != nil {
		t.Fatal(err)
	}
	a := Action{Kind: "copy", Source: &entry}
	previous := runTransferWorker
	defer func() { runTransferWorker = previous }()
	var partial string
	calls := 0
	runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, _ int64) error {
		if kind == "probe" {
			reportProgress(ctx, transferProgress{Total: 8, ETag: `"fixture"`})
			return nil
		}
		calls++
		if calls == 1 {
			partial = to
			reportProgress(ctx, transferProgress{ETag: `"fixture"`})
			if err := os.WriteFile(to, []byte("abc"), 0600); err != nil {
				return err
			}
			if err := os.WriteFile(to+pcsdownload.DownloadSuffix, []byte("fixture-state"), 0600); err != nil {
				return err
			}
			return context.Canceled
		}
		if to != partial {
			t.Error("partial path changed between attempts")
		}
		settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
		if settings.ETag != `"fixture"` {
			t.Error("entity validator not restored")
		}
		data, _ := os.ReadFile(to)
		if string(data) != "abc" {
			t.Errorf("partial discarded: %q", data)
		}
		return os.WriteFile(to, []byte("abcdefgh"), 0600)
	}
	if err := copyAction(context.Background(), src, dst, &a, opts()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst.Root); !os.IsNotExist(err) {
		t.Fatal("partial data published as completed file")
	}
	if err := copyAction(context.Background(), src, dst, &a, opts()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst.Root)
	if string(data) != "abcdefgh" {
		t.Fatalf("wrong final bytes: %q", data)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("published payload retained partial")
	}
	if _, err := os.Stat(partial + pcsdownload.DownloadSuffix); !os.IsNotExist(err) {
		t.Fatal("completed transfer retained range state")
	}
	local, err := dst.FS.Stat(context.Background(), dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, dst.Root, entry, local); err != nil || !ok {
		t.Fatalf("completed verification record invalid: %v %v", ok, err)
	}
	runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, _ string, _ int64) error {
		if kind != "probe" {
			t.Fatalf("unexpected operation: %s", kind)
		}
		reportProgress(ctx, transferProgress{Total: 8, ETag: `"changed-with-same-metadata"`})
		return nil
	}
	if ok, _ := verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, dst.Root, entry, local); ok {
		t.Fatal("changed live entity trusted despite unchanged metadata")
	}
	runTransferWorker = func(ctx context.Context, _ string, _ uint64, _, _ string, _ int64) error {
		t.Error("tampered local contents reached remote probe")
		reportProgress(ctx, transferProgress{Total: 8, ETag: `"fixture"`})
		return nil
	}
	if err := os.WriteFile(dst.Root, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, dst.Root, entry, local); ok {
		t.Fatal("modified destination trusted by completed record")
	}
}

func TestDownloadLeaseIsExclusiveAndReleases(t *testing.T) {
	name := filepath.Join(t.TempDir(), "download.lock")
	first, err := lockDownload(name)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if other, err := lockDownload(name); err == nil {
		other.Close()
		t.Fatal("two transfers acquired the same partial")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := lockDownload(name)
	if err != nil {
		t.Fatal("released lock remained busy:", err)
	}
	next.Close()
}

type editingPublishedLocal struct {
	Local
	target string
	edited bool
}

func (l *editingPublishedLocal) Stat(ctx context.Context, name string) (Entry, error) {
	if name == l.target && !l.edited {
		if _, err := os.Stat(name); err == nil {
			if err := os.WriteFile(name, []byte("modified"), 0600); err != nil {
				return Entry{}, err
			}
			l.edited = true
		}
	}
	return l.Local.Stat(ctx, name)
}

func TestCompletionRecordCannotBlessPostPublicationEdits(t *testing.T) {
	cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("original")}, id: 101}
	src := cloud.endpoint(7, "/Temp/a")
	target := filepath.Join(t.TempDir(), "result.bin")
	local := &editingPublishedLocal{target: target}
	dst := Endpoint{local, target}
	entry, err := src.FS.Stat(context.Background(), src.Root)
	if err != nil {
		t.Fatal(err)
	}
	previous := runTransferWorker
	defer func() { runTransferWorker = previous }()
	runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, _ int64) error {
		if kind == "probe" {
			reportProgress(ctx, transferProgress{Total: 8, ETag: `"fixture"`})
			return nil
		}
		reportProgress(ctx, transferProgress{ETag: `"fixture"`})
		return os.WriteFile(to, []byte("original"), 0600)
	}
	a := Action{Kind: "copy", Source: &entry}
	if err := copyAction(context.Background(), src, dst, &a, opts()); err != nil {
		t.Fatal(err)
	}
	if !local.edited {
		t.Fatal("publication edit fixture did not run")
	}
	current, err := (&Local{}).Stat(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, target, entry, current); err != nil || ok {
		t.Fatalf("edited destination accepted as the downloaded entity: ok=%v err=%v", ok, err)
	}
}

func TestLegacyCompletionRecordRequiresFullComparison(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			remote := &Remote{UID: 7}
			target := filepath.Join(t.TempDir(), "result.bin")
			entry := Entry{Size: 8, ID: "original", ModTime: time.Unix(100, 0)}
			stage, err := stageFor(context.Background(), remote, "/file", target, entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("modified"), 0600); err != nil {
				t.Fatal(err)
			}
			current, err := (&Local{}).Stat(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := hashSHA256(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			stage.info.ETag = `"original"`
			stage.info.Version = version
			stage.info.LocalSize = current.Size
			stage.info.LocalModified = current.ModTime.UnixNano()
			record, err := json.Marshal(completedDownload{Source: stage.info, SHA256: digest})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stage.record+".done", record, 0600); err != nil {
				t.Fatal(err)
			}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			runTransferWorker = func(ctx context.Context, _ string, _ uint64, _, _ string, _ int64) error {
				t.Error("legacy record reached the entity fast path")
				reportProgress(ctx, transferProgress{Total: 8, ETag: `"original"`})
				return nil
			}
			if ok, err := verifiedDownloadedFile(context.Background(), remote, "/file", target, entry, current); err != nil || ok {
				t.Fatalf("legacy published-file digest trusted: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestDownloadStateDirectoryOwnership(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".pcsclone-partials")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dir, "user-data")
	if err := os.WriteFile(userFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareStageDirectory(dir); err == nil {
		t.Fatal("unowned directory accepted")
	}
	files, err := (&Local{}).List(context.Background(), root)
	if err != nil || len(files) != 1 {
		t.Fatalf("user directory hidden: %v %v", files, err)
	}
	data, _ := os.ReadFile(userFile)
	if string(data) != "keep" {
		t.Fatal("user data changed")
	}
	ownedRoot := t.TempDir()
	owned := filepath.Join(ownedRoot, ".pcsclone-partials")
	if err := prepareStageDirectory(owned); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "partial.part"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err = (&Local{}).List(context.Background(), ownedRoot)
	if err != nil || len(files) != 0 {
		t.Fatalf("internal state exposed as user files: %v %v", files, err)
	}
}

func TestCanceledActionCannotSucceedWithLiveParent(t *testing.T) {
	for _, failCheckpoint := range []bool{false, true} {
		t.Run(fmt.Sprint(failCheckpoint), func(t *testing.T) {
			cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("fixture")}, id: 101}
			src := cloud.endpoint(1, "/Temp/a")
			entry, err := src.FS.Stat(context.Background(), src.Root)
			if err != nil {
				t.Fatal(err)
			}
			p := &Plan{Source: src, Destination: Endpoint{&Local{}, filepath.Join(t.TempDir(), "a")}, Actions: []Action{{Kind: "copy", Source: &entry}}}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			runTransferWorker = func(context.Context, string, uint64, string, string, int64) error {
				if failCheckpoint {
					return errors.Join(context.Canceled, ErrCheckpointSave)
				}
				return context.Canceled
			}
			r, err := Execute(context.Background(), p, opts())
			wantFailed, wantCanceled := 0, 1
			if failCheckpoint {
				wantFailed, wantCanceled = 1, 0
			}
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrCheckpointSave) != failCheckpoint || r.Failed != wantFailed || r.Canceled != wantCanceled {
				t.Fatalf("result=%+v err=%v", r, err)
			}
		})
	}
}

func TestDownloadStageRejectsChangedIdentityAndLocalData(t *testing.T) {
	for _, mode := range []string{"remote", "local", "missing-validator", "weak-validator"} {
		t.Run(mode, func(t *testing.T) {
			remote := &Remote{UID: 7}
			target := filepath.Join(t.TempDir(), "result.bin")
			entry := Entry{Size: 8, ID: "original", ModTime: time.Unix(100, 0)}
			stage, err := stageFor(context.Background(), remote, "/file", target, entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stage.payload, []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stage.payload+pcsdownload.DownloadSuffix, []byte("state"), 0600); err != nil {
				t.Fatal(err)
			}
			stage.info.ETag = `"v1"`
			if mode == "missing-validator" {
				stage.info.ETag = ""
			}
			if mode == "weak-validator" {
				stage.info.ETag = `W/"v1"`
				stage.info.LastModified = "Wed, 21 Oct 2015 07:28:00 GMT"
			}
			if err := stage.save(); err != nil {
				t.Fatal(err)
			}
			if mode == "remote" {
				entry.ID = "replacement"
			}
			if mode == "local" {
				if err := os.WriteFile(stage.payload, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			next, err := stageFor(context.Background(), remote, "/file", target, entry)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(next.payload); !os.IsNotExist(err) {
				t.Fatal("unsafe partial retained")
			}
		})
	}
}

func TestWorkerProgressAndGracefulCancelIPC(t *testing.T) {
	for _, tc := range []struct {
		kind string
		fail bool
	}{{"download", false}, {"download", true}, {"upload", false}, {"upload", true}} {
		kind, failCheckpoint := tc.kind, tc.fail
		t.Run(fmt.Sprintf("%s/%v", kind, failCheckpoint), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checkpoint := filepath.Join(t.TempDir(), "checkpoint")
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerIPCFixture$")
			cmd.Env = append(os.Environ(), "PCSCLONE_IPC_FIXTURE=1", "PCSCLONE_IPC_CHECKPOINT="+checkpoint)
			if failCheckpoint {
				cmd.Env = append(cmd.Env, "PCSCLONE_IPC_FAIL_CHECKPOINT=1")
			}
			seen := make(chan struct{}, 1)
			ctx = context.WithValue(ctx, progressKey{}, func(v transferProgress) {
				if v.Done == 128 {
					select {
					case seen <- struct{}{}:
					default:
					}
				}
			})
			done := make(chan error, 1)
			go func() { done <- executeWorkerCommand(ctx, cmd, kind, 7) }()
			select {
			case <-seen:
			case <-time.After(3 * time.Second):
				cancel()
				<-done
				t.Fatal("numeric progress not received")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if errors.Is(err, ErrCheckpointSave) != failCheckpoint {
					t.Fatalf("checkpoint failure lost across cancellation: %v", err)
				}
			case <-time.After(7 * time.Second):
				t.Fatal("child not joined")
			}
			if failCheckpoint {
				return
			}
			data, err := os.ReadFile(checkpoint)
			if err != nil || string(data) != "saved" {
				t.Fatalf("child killed before checkpoint: %q %v", data, err)
			}
		})
	}
}
func TestWorkerIPCFixture(t *testing.T) {
	if os.Getenv("PCSCLONE_IPC_FIXTURE") != "1" {
		return
	}
	fmt.Println(`@pcsclone {"Done":128,"Total":1024,"Rate":64}`)
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		if strings.TrimSpace(s.Text()) == "cancel" {
			if os.Getenv("PCSCLONE_IPC_FAIL_CHECKPOINT") == "1" {
				fmt.Println(`@pcsclone {"CheckpointFailed":true}`)
				os.Exit(1)
			}
			if os.WriteFile(os.Getenv("PCSCLONE_IPC_CHECKPOINT"), []byte("saved"), 0600) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
	os.Exit(3)
}

func TestProgressIncludesActiveAndResumedBytes(t *testing.T) {
	p := &Plan{Actions: []Action{{Kind: "copy", Path: "large", Source: &Entry{Size: 1024}}, {Kind: "copy", Path: "small", Source: &Entry{Size: 256}}}}
	s := newProgress(p)
	s.start(p.Actions[0])
	s.update("large", transferProgress{Done: 512, Rate: 128})
	snap := s.snapshot(Result{}, 1)
	if snap.Done != 512 || snap.Total != 1280 || snap.Rate != 128 || snap.ETA != 6*time.Second {
		t.Fatalf("wrong active progress: %+v", snap)
	}
	s.finish(p.Actions[0], true)
	s.start(p.Actions[1])
	s.update("small", transferProgress{Done: 128, Rate: 64})
	snap = s.snapshot(Result{Copied: 1}, 1)
	if snap.Done != 1152 || !strings.Contains(snap.String(), "90.0%") {
		t.Fatal(snap.String())
	}
	var b bytes.Buffer
	w := workerOutput{ctx: context.WithValue(context.Background(), progressKey{}, func(v transferProgress) { fmt.Fprint(&b, v.Done) })}
	w.Write([]byte("ignored diagnostics\n@pcsclone {\"Done\":"))
	w.Write([]byte("7}\n"))
	if b.String() != "7" {
		t.Fatal("fragmented worker output not decoded")
	}
	if downloadThreads(Options{Transfers: 4, DownloadThreads: 8, MaxConnections: 16}) != 4 {
		t.Fatal("connection budget not applied")
	}
}
