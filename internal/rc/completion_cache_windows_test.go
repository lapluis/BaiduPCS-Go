package rc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLockedCompletionReplacementFallsBackWithoutTrustingStaleDigest(t *testing.T) {
	src, dst, source, local, stage := completionProbeFixture(t)
	record := stage.record + ".done"
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(record)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	blocker := os.NewFile(uintptr(handle), record)
	defer blocker.Close()
	// Preserve size and mtime so that the old record can only be rejected by
	// comparing the actual bytes, rather than by a cheap metadata mismatch.
	if err := os.WriteFile(dst.Root, []byte("changed!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dst.Root, local.ModTime, local.ModTime); err != nil {
		t.Fatal(err)
	}
	digest, err := hashSHA256(context.Background(), dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	var renameErr *os.LinkError
	if err := stage.complete(context.Background(), dst.Root, digest); !errors.As(err, &renameErr) || renameErr.Op != "rename" || renameErr.New != record {
		t.Fatalf("expected locked completion replacement error: %v", err)
	}
	after, err := os.ReadFile(record)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed replacement changed old record: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(record))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".complete-") {
			t.Fatalf("temporary completion record leaked: %s", entry.Name())
		}
	}
	previous := runTransferWorker
	defer func() { runTransferWorker = previous }()
	probes, downloads := 0, 0
	runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, _ int64) error {
		if kind == "probe" {
			probes++
			reportProgress(ctx, transferProgress{Total: 8, ETag: `"refreshed"`})
			return nil
		}
		downloads++
		return os.WriteFile(to, []byte("fixture!"), 0600)
	}
	plan, err := FilePlan(context.Background(), src, dst, false, opts())
	if err != nil || plan == nil || len(plan.Actions) != 1 || plan.Actions[0].Kind != "copy" || probes != 0 || downloads != 1 {
		t.Fatalf("stale digest bypassed comparison: plan=%v probes=%d downloads=%d err=%v", plan, probes, downloads, err)
	}
	if data, err := os.ReadFile(dst.Root); err != nil || string(data) != "changed!" {
		t.Fatalf("planning modified destination: %q %v", data, err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst.Root, []byte("fixture!"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err = hashSHA256(context.Background(), dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	stage.info.ETag = `"refreshed"`
	if err := stage.complete(context.Background(), dst.Root, digest); err != nil {
		t.Fatal("completion did not recover after releasing handle", err)
	}
	local, err = dst.FS.Stat(context.Background(), dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := verifiedDownloadedFile(context.Background(), src.FS.(*Remote), src.Root, dst.Root, source, local); err != nil || !ok || probes != 1 || downloads != 1 {
		t.Fatalf("refreshed completion was not verified: ok=%v probes=%d downloads=%d err=%v", ok, probes, downloads, err)
	}
}
