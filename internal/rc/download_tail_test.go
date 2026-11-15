package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

func TestDownloadTailWindows(t *testing.T) {
	for _, mode := range []string{"slow-tail", "slow-batch", "short-tail", "intermittent", "idle", "irregular-heartbeats", "restored-offset"} {
		t.Run(mode, func(t *testing.T) {
			sampler := downloadTail{isLastAfterFastPeer: func() bool { return mode != "slow-batch" }}
			now := time.Unix(100, 0)
			done := int64(128 << 20)
			total := int64(512 << 20)
			for i := 0; i < 14; i++ {
				delta := int64(1 << 20)
				step := 5 * time.Second
				switch mode {
				case "short-tail":
					total = done + 16<<20
				case "intermittent":
					if i%4 == 0 {
						delta = 4 << 20
					}
				case "idle":
					delta = 0
				case "irregular-heartbeats":
					step = 13 * time.Second
				case "restored-offset":
					if i > 0 {
						delta = 0
					}
				}
				done += delta
				got := sampler.observe(now, transferProgress{Done: done, Total: total})
				want := mode == "slow-tail" && i >= 7
				if got != want {
					t.Fatalf("sample %d trigger=%v want %v", i, got, want)
				}
				now = now.Add(step)
			}
			if sampler.fastPeerCandidate {
				t.Fatal("counted resumed offset as fast network bytes")
			}
		})
	}
}

func TestDownloadTailReconnectUsesCheckpoint(t *testing.T) {
	for _, mode := range []string{"resume", "limited", "no-retries", "checkpoint-error", "no-validator", "no-checkpoint", "uniformly-slow", "caller-canceled"} {
		t.Run(mode, func(t *testing.T) {
			// Metadata supplies synthetic file sizes. Sparse local outputs keep the
			// test cheap; the worker checks that its checkpoint survived the restart.
			payload := make([]byte, 64<<20)
			cloud := &fakeCloud{files: map[string][]byte{"/Temp/fast": payload, "/Temp/tail": payload}, id: 71}
			src := cloud.endpoint(7, "/Temp")
			dst := Endpoint{&Local{}, t.TempDir()}
			p := &Plan{Source: src, Destination: dst}
			for _, name := range []string{"fast", "tail"} {
				entry, err := src.FS.Stat(context.Background(), src.At(name))
				if err != nil {
					t.Fatal(err)
				}
				p.Actions = append(p.Actions, Action{Kind: "copy", Path: name, Source: &entry})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ready := make(chan struct{})
			o := opts()
			o.Retries = 2
			if mode == "limited" {
				o.BWLimit = 1 << 20
			}
			if mode == "no-retries" {
				o.Retries = 0
			}
			var reconnects, attempts int
			o.Emit = func(kind, item string) {
				if kind == "copy" && item == "fast" {
					close(ready)
				}
				if kind == "reconnect" {
					reconnects++
				}
			}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			// Only the fast worker samples until its copy is published, then only
			// the tail samples. Advancing the clock avoids timing-dependent tests.
			now := time.Unix(100, 0)
			runTransferWorker = func(wctx context.Context, kind string, _ uint64, from, to string, _ int64) error {
				if kind != "download" {
					return fmt.Errorf("unexpected worker %s", kind)
				}
				if from == "/Temp/tail" {
					select {
					case <-ready:
					case <-ctx.Done():
						return ctx.Err()
					}
					attempts++
				}
				v := transferProgress{Total: int64(len(payload)), Modified: "fixture-date"}
				if mode == "no-validator" {
					v.Modified = ""
				}
				if mode != "no-validator" {
					reportProgress(wctx, v)
				}
				if from == "/Temp/tail" && attempts == 2 {
					settings := wctx.Value(transferSettingsKey{}).(workerSettings)
					data, err := os.ReadFile(to)
					if err != nil || string(data) != "prefix" || settings.Modified != "fixture-date" {
						return errors.New("partial or validator lost")
					}
					if data, err = os.ReadFile(to + pcsdownload.DownloadSuffix); err != nil || string(data) != "saved ranges" {
						return errors.New("range checkpoint lost")
					}
					if wctx.Value(downloadTailKey{}) != nil {
						return errors.New("rearmed reconnect on second attempt")
					}
				} else {
					if err := os.WriteFile(to, []byte("prefix"), 0600); err != nil {
						return err
					}
					if mode != "no-checkpoint" {
						if err := os.WriteFile(to+pcsdownload.DownloadSuffix, []byte("saved ranges"), 0600); err != nil {
							return err
						}
					}
					v.Modified = ""
					v.Done = 1
					reportProgress(wctx, v)
					for i := 0; i < 10; i++ {
						now = now.Add(5 * time.Second)
						v.Done += 1 << 20
						if from == "/Temp/fast" && mode != "uniformly-slow" {
							v.Done += 24 << 20
						}
						if v.Done >= v.Total {
							break
						}
						reportProgress(wctx, v)
						if wctx.Err() != nil {
							if mode == "checkpoint-error" {
								return errors.Join(wctx.Err(), ErrCheckpointSave)
							}
							if mode == "caller-canceled" {
								cancel()
							}
							return wctx.Err()
						}
					}
				}
				return os.Truncate(to, int64(len(payload)))
			}
			result, err := execute(ctx, p, o, func() time.Time { return now })
			switch mode {
			case "checkpoint-error":
				if !errors.Is(err, ErrCheckpointSave) || attempts != 1 || result.Failed != 1 || reconnects != 0 {
					t.Fatalf("checkpoint failure: %+v %v", result, err)
				}
			case "caller-canceled":
				if !errors.Is(err, context.Canceled) || attempts != 1 || result.Canceled != 1 || reconnects != 0 {
					t.Fatalf("caller cancellation: %+v %v", result, err)
				}
			default:
				want := 0
				if mode == "resume" {
					want = 1
				}
				if err != nil || result.Copied != 2 || result.Canceled != 0 || reconnects != want || attempts != 1+want {
					t.Fatalf("result=%+v err=%v reconnects=%d attempts=%d", result, err, reconnects, attempts)
				}
				if _, err := os.Stat(filepath.Join(dst.Root, "tail")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
