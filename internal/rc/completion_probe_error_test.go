package rc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func completionProbeFixture(t *testing.T) (Endpoint, Endpoint, Entry, Entry, downloadStage) {
	t.Helper()
	cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("fixture!")}, id: 101}
	src := cloud.endpoint(7, "/Temp/a")
	dst := Endpoint{&Local{}, filepath.Join(t.TempDir(), "target")}
	if err := os.WriteFile(dst.Root, []byte("fixture!"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := src.FS.Stat(context.Background(), src.Root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := dst.FS.Stat(context.Background(), dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := stageFor(context.Background(), src.FS.(*Remote), src.Root, dst.Root, a)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := hashSHA256(context.Background(), dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	stage.info.ETag = `"fixture"`
	if err := stage.complete(context.Background(), dst.Root, digest); err != nil {
		t.Fatal(err)
	}
	return src, dst, a, b, stage
}

func TestCompletionProbePreservesFatalErrors(t *testing.T) {
	for _, surface := range []string{"verify", "plan"} {
		for _, marker := range []error{ErrCheckpointSave, ErrWorkerProtocol, ErrProgressCallback, ErrLogCallback} {
			for _, canceled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/cancel=%t", surface, marker, canceled), func(t *testing.T) {
					src, dst, a, b, stage := completionProbeFixture(t)
					before, err := os.ReadFile(stage.record + ".done")
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					cause := errors.New("fixture probe failure")
					failure := fmt.Errorf("probe: %w", errors.Join(marker, cause))
					previous := runTransferWorker
					defer func() { runTransferWorker = previous }()
					probes, downloads := 0, 0
					runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, _ int64) error {
						if kind == "probe" {
							probes++
							if canceled {
								cancel()
							}
							return failure
						}
						downloads++
						return os.WriteFile(to, []byte("fixture!"), 0600)
					}
					if surface == "verify" {
						var verified bool
						verified, err = verifiedDownloadedFile(ctx, src.FS.(*Remote), src.Root, dst.Root, a, b)
						if verified {
							t.Error("failed probe certified completion")
						}
					} else {
						var plan *Plan
						plan, err = FilePlan(ctx, src, dst, false, opts())
						if plan != nil {
							t.Error("fatal probe failure produced a plan")
						}
					}
					if !errors.Is(err, marker) || !errors.Is(err, cause) || errors.Is(err, context.Canceled) != canceled || probes != 1 || downloads != 0 {
						t.Fatalf("fatal probe error swallowed: probes=%d downloads=%d err=%v", probes, downloads, err)
					}
					after, err := os.ReadFile(stage.record + ".done")
					if err != nil || !bytes.Equal(before, after) {
						t.Fatalf("completion record changed: %v", err)
					}
					if data, err := os.ReadFile(dst.Root); err != nil || string(data) != "fixture!" {
						t.Fatalf("destination changed: data=%q err=%v", data, err)
					}
				})
			}
		}
	}
}

func TestCompletionProbeOrdinaryFailureStillComparesContent(t *testing.T) {
	for _, mode := range []string{"healthy", "ordinary-error", "no-validator", "changed-validator"} {
		t.Run(mode, func(t *testing.T) {
			src, dst, _, _, _ := completionProbeFixture(t)
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			probes, downloads := 0, 0
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, _ int64) error {
				if kind == "probe" {
					probes++
					if mode == "ordinary-error" {
						return errors.New("fixture temporary probe failure")
					}
					etag := `"fixture"`
					if mode == "no-validator" {
						etag = ""
					} else if mode == "changed-validator" {
						etag = `"new"`
					}
					reportProgress(ctx, transferProgress{Total: 8, ETag: etag})
					return nil
				}
				downloads++
				return os.WriteFile(to, []byte("fixture!"), 0600)
			}
			plan, err := FilePlan(context.Background(), src, dst, false, opts())
			wantDownloads := 1
			if mode == "healthy" {
				wantDownloads = 0
			}
			if err != nil || plan == nil || len(plan.Actions) != 1 || plan.Actions[0].Kind != "skip" || probes != 1 || downloads != wantDownloads {
				t.Fatalf("comparison fallback changed: plan=%v probes=%d downloads=%d err=%v", plan, probes, downloads, err)
			}
		})
	}
}
