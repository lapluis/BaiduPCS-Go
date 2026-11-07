package rc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSingleLocalTransferDoesNotDivideByUnusedSlots(t *testing.T) {
	s, d := endpoints(t)
	content := bytes.Repeat([]byte("data"), 16*1024)
	if err := os.WriteFile(s.At("file"), content, 0600); err != nil {
		t.Fatal(err)
	}
	write(t, d, "file", "old target")
	s.Root, d.Root = s.At("file"), d.At("file")
	o := Options{Transfers: 32, MaxConnections: 32, BWLimit: 128 * 1024, MaxDelete: -1}
	plan, err := FilePlan(context.Background(), s, d, false, o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	result, err := Execute(ctx, plan, o)
	// This body takes half a second at the requested rate, and sixteen seconds
	// if it is incorrectly divided among 32 configured but unused slots.
	if err != nil || result != (Result{Copied: 1, Bytes: int64(len(content))}) {
		t.Fatalf("unused slots reduced local bandwidth: result=%+v err=%v", result, err)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Error("bandwidth limit was ignored")
	}
	for _, name := range []string{s.Root, d.Root} {
		if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, content) {
			t.Errorf("local copy data mismatch: %s (%v)", name, err)
		}
	}
}

func TestTransferBudgetUsesPlannedFileCount(t *testing.T) {
	for _, tc := range []struct {
		limit                                                   int64
		name, mode                                              string
		files, transfers, budget, threads, slots, workerThreads int
		rate                                                    int64
	}{
		{128, "file-copy", "copy", 1, 4, 16, 16, 1, 16, 128},
		{128, "file-move", "move", 1, 4, 16, 16, 1, 16, 128},
		{128, "file-checksum", "checksum", 1, 4, 16, 16, 1, 16, 128},
		{128, "two-files", "copy", 2, 4, 16, 16, 2, 8, 64},
		{128, "full-queue", "copy", 8, 4, 16, 16, 4, 4, 32},
		{128, "capped-budget", "copy", 8, 8, 2, 8, 2, 1, 64},
		{128, "default-budget", "copy", 20, 64, 0, 4, 16, 1, 8},
		{128, "default-options", "copy", 1, 0, 0, 0, 1, 4, 128},
		{128, "cancel-admitted", "cancel", 8, 8, 2, 8, 2, 1, 64},
		{128, "identical-move", "skip-move", 1, 4, 16, 16, 1, 16, 128},
		{128, "upload", "upload", 1, 4, 16, 16, 1, 16, 128},
		{128, "upload-checksum", "checksum-upload", 1, 4, 16, 16, 1, 16, 128},
		{128, "one-copy-many-skips", "mixed-copy", 9, 4, 16, 16, 1, 16, 128},
		{128, "one-move-many-ignored", "mixed-ignore-move", 9, 4, 16, 16, 1, 16, 128},
		{1, "low-one", "copy", 4, 4, 8, 4, 1, 4, 1},
		{2, "low-two", "copy", 4, 4, 8, 4, 2, 4, 1},
		{3, "low-three", "copy", 4, 4, 8, 4, 3, 2, 1},
		{1, "low-move", "move", 4, 4, 8, 4, 1, 4, 1},
		{1, "low-cancel", "cancel", 4, 4, 8, 4, 1, 4, 1},
		{0, "unlimited", "copy", 4, 4, 8, 4, 4, 2, 0},
		{math.MaxInt64, "large-bandwidth", "copy", 4, 4, 8, 4, 4, 2, math.MaxInt64 / 4},
		{1, "low-mixed-copy", "mixed-copy", 9, 4, 16, 16, 1, 16, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			var s, d Endpoint
			var cloud *fakeCloud
			upload := strings.Contains(tc.mode, "upload")
			var sourcePaths, targetPaths []string
			if upload {
				s = Endpoint{&Local{}, t.TempDir()}
				write(t, s, "file", "data")
				s.Root = s.At("file")
				cloud = &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("old!")}, id: 101}
				d = cloud.endpoint(7, "/Temp/a")
				sourcePaths, targetPaths = []string{s.Root}, []string{d.Root}
			} else {
				s, d = hashBudgetFixture(t, tc.files)
				for i := 0; i < tc.files; i++ {
					name := fmt.Sprintf("%02d", i)
					sourcePaths = append(sourcePaths, s.At(name))
					targetPaths = append(targetPaths, d.At(name))
					if tc.mode == "mixed-ignore-move" && i == 0 {
						if err := os.Remove(d.At(name)); err != nil {
							t.Fatal(err)
						}
					} else if tc.mode != "skip-move" && !(tc.mode == "mixed-copy" && i > 0) {
						write(t, d, name, "old!")
					}
				}
				if tc.files == 1 {
					s.Root, d.Root = sourcePaths[0], targetPaths[0]
				}
			}
			runTransferWorker = func(_ context.Context, kind string, _ uint64, from, to string, _ int64) error {
				if kind != "download" {
					return fmt.Errorf("unexpected planning worker: %s", kind)
				}
				data := []byte("data")
				if cloud != nil {
					data = cloud.files[from]
				}
				return os.WriteFile(to, data, 0600)
			}
			o := Options{Transfers: tc.transfers, MaxConnections: tc.budget, DownloadThreads: tc.threads, Checkers: 4, BWLimit: tc.limit, MaxDelete: -1, Checksum: strings.Contains(tc.mode, "checksum")}
			o.IgnoreExisting = tc.mode == "mixed-ignore-move"
			move := tc.mode == "move" || tc.mode == "skip-move" || tc.mode == "mixed-ignore-move"
			var plan *Plan
			var err error
			if tc.files == 1 {
				plan, err = FilePlan(context.Background(), s, d, move, o)
			} else {
				mode := "copy"
				if move {
					mode = "move"
				}
				plan, err = BuildPlan(context.Background(), s, d, mode, o)
			}
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := tc.files
			if tc.mode == "move" || o.Checksum {
				wantCalls += tc.files
			}
			if tc.mode == "mixed-copy" {
				wantCalls = 1
			}
			if tc.mode == "mixed-ignore-move" {
				wantCalls = 2
			}
			started, release := make(chan struct{}, tc.files*3), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			var mu sync.Mutex
			active, peak, connections, peakConnections, calls := 0, 0, 0, 0, 0
			var allocated, peakAllocated int64
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, from, to string, rate int64) error {
				settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
				if settings.Threads != tc.workerThreads || rate != tc.rate {
					t.Errorf("%s worker threads=%d rate=%d, want threads=%d rate=%d", kind, settings.Threads, rate, tc.workerThreads, tc.rate)
				}
				used := max(1, settings.Threads)
				if kind == "upload" {
					used = 1
				}
				mu.Lock()
				calls++
				active++
				connections += used
				peak = max(peak, active)
				peakConnections = max(peakConnections, connections)
				allocated += rate
				peakAllocated = max(peakAllocated, allocated)
				mu.Unlock()
				defer func() { mu.Lock(); active--; connections -= used; allocated -= rate; mu.Unlock() }()
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if kind == "upload" {
					data, err := os.ReadFile(from)
					if err != nil {
						return err
					}
					remote := d.FS.(*Remote)
					remote.mu.Lock()
					cloud.files[to] = data
					remote.mu.Unlock()
					return nil
				}
				if kind != "download" {
					return fmt.Errorf("unexpected transfer worker: %s", kind)
				}
				data := []byte("data")
				if cloud != nil {
					remote := d.FS.(*Remote)
					remote.mu.Lock()
					data = append([]byte(nil), cloud.files[from]...)
					remote.mu.Unlock()
				}
				return os.WriteFile(to, data, 0600)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			done := make(chan struct{})
			var result Result
			var runErr error
			go func() { result, runErr = Execute(ctx, plan, o); close(done) }()
			defer func() { unblock(); cancel(); <-done }()
			for i := 0; i < tc.slots; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("transfer slots did not start")
				}
			}
			if tc.files > tc.slots {
				select {
				case <-started:
					t.Error("too many admitted transfers")
				case <-time.After(25 * time.Millisecond):
				}
			}
			canceled := tc.mode == "cancel"
			if canceled {
				cancel()
				wantCalls = tc.slots
			} else {
				unblock()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("transfer did not join")
			}
			want := Result{Copied: tc.files, Bytes: int64(4 * tc.files)}
			if canceled {
				want = Result{Canceled: tc.slots, Pending: tc.files - tc.slots}
			}
			if tc.mode == "skip-move" {
				want = Result{Skipped: tc.files}
			}
			if tc.mode == "mixed-copy" || tc.mode == "mixed-ignore-move" {
				want = Result{Copied: 1, Skipped: tc.files - 1, Bytes: 4}
			}
			if errors.Is(runErr, context.Canceled) != canceled || !canceled && runErr != nil || result != want {
				t.Errorf("result=%+v err=%v, want %+v", result, runErr, want)
			}
			budget := tc.budget
			if budget < 1 {
				budget = 16
			}
			if active != 0 || calls != wantCalls || peak != tc.slots || peakConnections > budget {
				t.Errorf("active=%d calls=%d peak=%d connections=%d, want calls=%d slots=%d budget=%d", active, calls, peak, peakConnections, wantCalls, tc.slots, budget)
			}
			if allocated != 0 || tc.limit > 0 && peakAllocated > tc.limit {
				t.Errorf("transfer bandwidth allocation current=%d peak=%d exceeds %d", allocated, peakAllocated, tc.limit)
			}
			for i, p := range targetPaths {
				wantData := "data"
				if canceled || tc.mode == "mixed-ignore-move" && i > 0 {
					wantData = "old!"
				}
				var data []byte
				var err error
				if cloud != nil {
					data = cloud.files[p]
				} else {
					data, err = os.ReadFile(p)
				}
				if err != nil || string(data) != wantData {
					t.Errorf("target=%q err=%v, want %q", data, err, wantData)
				}
			}
			for i, p := range sourcePaths {
				_, err := s.FS.Stat(context.Background(), p)
				if move && !canceled && (tc.mode != "mixed-ignore-move" || i == 0) {
					if !errors.Is(err, os.ErrNotExist) {
						t.Errorf("verified move retained source: %v", err)
					}
				} else if err != nil {
					t.Errorf("transfer lost source: %v", err)
				}
			}
		})
	}
}
