package rc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

const oversizedDownloadRecordSize = 32 << 20

func enlargeDownloadRecord(t *testing.T, name string) os.FileInfo {
	t.Helper()
	f, err := os.OpenFile(name, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(oversizedDownloadRecordSize); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func checkOversizedRecordPreserved(t *testing.T, name string, before os.FileInfo) {
	t.Helper()
	after, err := os.Stat(name)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		t.Errorf("oversized record was modified: info=%v err=%v", after, err)
	}
}

func TestOversizedResumeRecordPreservesProgress(t *testing.T) {
	f := newStageReadFixture(t)
	before := enlargeDownloadRecord(t, f.stage.record)
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	_, err := stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
	runtime.ReadMemStats(&end)
	if !errors.Is(err, errDownloadRecordSize) {
		t.Errorf("oversized record was not rejected as an inspection failure: %v", err)
	}
	if allocated := end.TotalAlloc - start.TotalAlloc; allocated > 8<<20 {
		t.Errorf("inspection allocated %d bytes for oversized metadata", allocated)
	}
	checkOversizedRecordPreserved(t, f.stage.record, before)
	// Repair only the inspected metadata; the preserved payload and ranges
	// must still be usable without restarting a transfer.
	if err := os.WriteFile(f.stage.record, f.before[f.stage.record], 0600); err != nil {
		t.Fatal(err)
	}
	f.checkPreservedAndRetry(t)
}

func TestOversizedCompletionRecordStopsComparison(t *testing.T) {
	for _, surface := range []string{"verify", "plan"} {
		t.Run(surface, func(t *testing.T) {
			src, dst, a, b, stage := completionProbeFixture(t)
			name := stage.record + ".done"
			original, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			before := enlargeDownloadRecord(t, name)
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			calls := 0
			runTransferWorker = func(context.Context, string, uint64, string, string, int64) error {
				calls++
				return errors.New("unexpected worker")
			}
			var start, end runtime.MemStats
			runtime.ReadMemStats(&start)
			if surface == "verify" {
				var verified bool
				verified, err = verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, dst.Root, a, b)
				if verified {
					t.Error("oversized completion record certified destination")
				}
			} else {
				var plan *Plan
				plan, err = FilePlan(context.Background(), src, dst, false, opts())
				if plan != nil {
					t.Error("oversized completion record produced a plan")
				}
			}
			runtime.ReadMemStats(&end)
			if !errors.Is(err, errDownloadRecordSize) || calls != 0 {
				t.Errorf("inspection failure fell through to network I/O: calls=%d err=%v", calls, err)
			}
			if allocated := end.TotalAlloc - start.TotalAlloc; allocated > 8<<20 {
				t.Errorf("comparison allocated %d bytes for oversized metadata", allocated)
			}
			checkOversizedRecordPreserved(t, name, before)
			if data, err := os.ReadFile(dst.Root); err != nil || string(data) != "fixture!" {
				t.Fatalf("inspection changed destination: %q %v", data, err)
			}
			if err := os.WriteFile(name, original, 0600); err != nil {
				t.Fatal(err)
			}
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, _ string, _ int64) error {
				calls++
				if kind != "probe" {
					t.Fatalf("repair unexpectedly downloaded content: %s", kind)
				}
				reportProgress(ctx, transferProgress{Total: a.Size, ETag: `"fixture"`})
				return nil
			}
			calls = 0
			verified, err := verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, dst.Root, a, b)
			if err != nil || !verified || calls != 1 {
				t.Fatalf("repaired completion record unusable: verified=%t calls=%d err=%v", verified, calls, err)
			}
		})
	}
}

type growingDownloadRecord struct {
	remaining, read int
}

func (r *growingDownloadRecord) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	clear(p[:n])
	r.remaining -= n
	r.read += n
	return n, nil
}

func TestDownloadRecordReadBudget(t *testing.T) {
	for _, size := range []int{0, maxDownloadRecordSize - 1, maxDownloadRecordSize, maxDownloadRecordSize + 1000} {
		r := &growingDownloadRecord{remaining: size}
		data, err := readDownloadRecordContents(r)
		if size <= maxDownloadRecordSize {
			if err != nil || len(data) != size || r.read != size {
				t.Fatalf("in-budget record changed: size=%d read=%d result=%d err=%v", size, r.read, len(data), err)
			}
		} else if !errors.Is(err, errDownloadRecordSize) || data != nil || r.read != maxDownloadRecordSize+1 {
			t.Fatalf("growing record escaped budget: read=%d result=%d err=%v", r.read, len(data), err)
		}
	}
}

func TestOversizedDownloadRecordWritesPreserveExistingRecords(t *testing.T) {
	for _, completion := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "completion"}[completion], func(t *testing.T) {
			f := newStageReadFixture(t)
			name := f.stage.record
			if err := os.WriteFile(f.target, []byte("fixture!"), 0600); err != nil {
				t.Fatal(err)
			}
			digest, err := hashSHA256(context.Background(), f.target)
			if err != nil {
				t.Fatal(err)
			}
			if completion {
				name += ".done"
				if err := f.stage.complete(context.Background(), f.target, digest); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			originalETag := f.stage.info.ETag
			f.stage.info.ETag = strings.Repeat("x", maxDownloadRecordSize+1)
			if completion {
				err = f.stage.complete(context.Background(), f.target, digest)
			} else {
				err = f.stage.save()
				if !errors.Is(err, ErrCheckpointSave) {
					t.Errorf("checkpoint save marker lost: %v", err)
				}
			}
			if !errors.Is(err, errDownloadRecordSize) {
				t.Fatalf("writer created an unreadable record: %v", err)
			}
			after, err := os.ReadFile(name)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed metadata write replaced existing record: %v", err)
			}
			f.stage.info.ETag = originalETag
			// Remove only this test's synthetic published file before asking the
			// fixture helper to verify the original resume files and retry.
			if err := os.Remove(f.target); err != nil {
				t.Fatal(err)
			}
			f.checkPreservedAndRetry(t)
		})
	}
}
