package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlEventFailuresJoinBeforeFinalization(t *testing.T) {
	for _, phase := range []string{"pause", "resume", "cancel", "probe", "late", "early"} {
		for _, mode := range []string{"typed", "string", "healthy"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestControlEventProcessFixture$")
				cmd.Env = append(os.Environ(), "PCS_CONTROL_EVENT="+phase+"/"+mode)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("control callback escaped or failed: %v\n%s", err, output)
				}
			})
		}
	}
}

func TestControlEventProcessFixture(t *testing.T) {
	mode := os.Getenv("PCS_CONTROL_EVENT")
	if mode == "" {
		return
	}
	phase, flavor, _ := strings.Cut(mode, "/")
	started := make(chan struct{})
	bodyReady := make(chan struct{})
	releaseBody := sync.OnceFunc(func() { close(bodyReady) })
	defer releaseBody()
	var startOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		if phase == "late" || phase == "early" {
			select {
			case <-bodyReady:
				if phase == "early" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Range", "bytes 0-7/8")
				w.Header().Set("Content-Length", "8")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("abcdefgh"))
			case <-r.Context().Done():
			}
		} else {
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	name := filepath.Join(t.TempDir(), "data")
	const original = "original output with tail"
	if err := os.WriteFile(name, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	state := name + ".state"
	d := NewDownloader(server.URL, file, &Config{MaxParallel: 1, InstanceStatePath: state, InstanceStateStorageFormat: InstanceStateStorageFormatProto3})
	if phase != "probe" && phase != "early" {
		d.SetFileContentLength(8)
	}
	entered, exited, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	defer release()
	var calls, finishes, successes atomic.Int32
	cause := errors.New("synthetic " + phase + " observer failure")
	callback := func() {
		calls.Add(1)
		close(entered)
		defer close(exited)
		<-released
		if phase == "early" && flavor == "healthy" {
			d.Cancel()
		}
		if flavor == "typed" {
			panic(cause)
		}
		if flavor == "string" {
			panic(cause.Error())
		}
	}
	switch phase {
	case "pause", "late", "early":
		d.OnPause(callback)
	case "resume":
		d.OnResume(callback)
	case "cancel", "probe":
		d.OnCancel(callback)
	default:
		t.Fatal("invalid fixture phase")
	}
	d.OnExecute(func() {
		if err := d.monitor.saveCheckpoint(); err != nil {
			t.Errorf("baseline: %v", err)
		}
	})
	d.OnSuccess(func() { successes.Add(1) })
	d.OnFinish(func() {
		finishes.Add(1)
		select {
		case <-exited:
		default:
			t.Error("finish preceded control observer exit")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.ExecuteWithContext(ctx) }()
	joined := false
	defer func() {
		release()
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("execution did not stop")
			}
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	switch phase {
	case "pause", "late", "early":
		d.Pause()
	case "resume":
		d.Pause()
		d.Resume()
	case "cancel", "probe":
		d.Cancel()
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("observer did not start")
	}
	if phase == "late" {
		d.Resume()
		releaseBody()
		select {
		case <-d.monitor.CompletedChan():
		case <-ctx.Done():
			t.Fatal("network did not complete while observer blocked")
		}
	}
	if phase == "early" {
		releaseBody()
		for {
			d.cancelMu.Lock()
			sealed := d.controlEvents != nil && !d.controlEvents.accepting
			d.cancelMu.Unlock()
			if sealed {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("early HTTP failure did not join accepted control")
			case <-time.After(time.Millisecond):
			}
		}
	}
	// Cancellation and worker completion must not bypass an accepted observer.
	select {
	case runErr := <-done:
		joined = true
		t.Fatalf("returned before observer: %v", runErr)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	<-exited
	if flavor == "healthy" && phase != "cancel" && phase != "probe" && phase != "late" && phase != "early" {
		d.Cancel()
	}
	var runErr error
	select {
	case runErr = <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("observer failure did not stop transfer")
	}
	completed := phase == "late" && flavor == "healthy"
	if errors.Is(runErr, context.Canceled) == completed || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, ErrEventCallback) != (flavor != "healthy") || completed && runErr != nil {
		t.Errorf("control error classification: %v", runErr)
	}
	if flavor != "healthy" && (runErr == nil || !strings.Contains(runErr.Error(), cause.Error()) || flavor == "typed" && !errors.Is(runErr, cause)) {
		t.Errorf("observer cause lost: %v", runErr)
	}
	if phase == "early" && (runErr == nil || !strings.Contains(runErr.Error(), "500")) {
		t.Errorf("early HTTP error lost: %v", runErr)
	}
	wantFinish, wantSuccess := int32(1), int32(0)
	if phase == "probe" || phase == "early" {
		wantFinish = 0
	}
	if completed {
		wantSuccess = 1
	}
	if calls.Load() != 1 || finishes.Load() != wantFinish || successes.Load() != wantSuccess {
		t.Errorf("events calls=%d finish=%d success=%d", calls.Load(), finishes.Load(), successes.Load())
	}
	wantData := original
	if phase == "late" {
		wantData = "abcdefgh" + original[8:]
	}
	if completed {
		wantData = "abcdefgh"
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != wantData {
		t.Errorf("output changed: %q %v", got, err)
	}
	// Completed controls must neither restart work nor notify an old observer.
	d.Pause()
	d.Resume()
	d.Cancel()
	if calls.Load() != 1 {
		t.Error("control after completion triggered stale callback")
	}
	if completed || phase == "probe" || phase == "early" {
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Errorf("unexpected checkpoint after completed transfer or initial probe: %v", err)
		}
		return
	}
	saved, err := os.Open(state)
	if err != nil {
		t.Fatal("checkpoint lost:", err)
	}
	reader := NewInstanceState(saved, InstanceStateStorageFormatProto3)
	defer reader.Close()
	info, err := reader.Load()
	wantDone, wantRemaining := int64(0), int64(8)
	if phase == "late" {
		wantDone, wantRemaining = 8, 0
	}
	if err != nil || info == nil || info.DownloadStatus.Downloaded() != wantDone || info.Ranges.Len() != wantRemaining {
		t.Fatalf("unfinished ranges lost: info=%v err=%v", info, err)
	}
	fmt.Print("control observer joined and progress retained\n")
}
