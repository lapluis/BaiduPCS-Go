package rc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type comparisonMutation struct {
	Local
	once   sync.Once
	change func()
	onList bool
}

func (b *comparisonMutation) Hash(ctx context.Context, p string) (string, error) {
	digest, err := b.Local.Hash(ctx, p)
	if err == nil && !b.onList {
		b.once.Do(b.change)
	}
	return digest, err
}

func (b *comparisonMutation) List(ctx context.Context, p string) ([]Entry, error) {
	items, err := b.Local.List(ctx, p)
	if err == nil && b.onList {
		b.once.Do(b.change)
	}
	return items, err
}

func TestComparisonRejectsChangesDuringHash(t *testing.T) {
	for _, mode := range []string{"check", "copy", "sync", "file-plan"} {
		for _, hashing := range []string{"source", "destination"} {
			for _, changed := range []string{"source", "destination"} {
				for _, mutation := range []string{"identity", "mtime"} {
					t.Run(mode+"/"+hashing+"/"+changed+"/"+mutation, func(t *testing.T) {
						s, d := endpoints(t)
						write(t, s, "file", "OLD!")
						write(t, d, "file", "OLD!")
						name := s.At("file")
						if changed == "destination" {
							name = d.At("file")
						}
						change := func() {
							if mutation == "identity" {
								replaceSameMetadata(t, name)
							} else {
								future := time.Now().Add(time.Hour)
								if err := os.Chtimes(name, future, future); err != nil {
									t.Error(err)
								}
							}
						}
						backend := &comparisonMutation{change: change}
						if hashing == "source" {
							s.FS = backend
						} else {
							d.FS = backend
						}
						o := opts()
						logs := 0
						o.Emit = func(string, string) { logs++ }
						var err error
						var plan *Plan
						if mode == "check" {
							err = Check(context.Background(), s, d, o)
						} else if mode == "file-plan" {
							s.Root, d.Root = s.At("file"), d.At("file")
							plan, err = FilePlan(context.Background(), s, d, false, o)
						} else {
							plan, err = BuildPlan(context.Background(), s, d, mode, o)
						}
						if err == nil || !strings.Contains(err.Error(), "changed after planning") || plan != nil || logs != 0 {
							t.Fatalf("unstable comparison accepted: plan=%v logs=%d err=%v", plan, logs, err)
						}
						want := "OLD!"
						if mutation == "identity" {
							want = "NEW!"
						}
						if got, err := os.ReadFile(name); err != nil || string(got) != want {
							t.Errorf("comparison rewrote changed file: %q %v", got, err)
						}
					})
				}
			}
		}
	}
}

func TestSizeOnlyComparisonRechecksInventory(t *testing.T) {
	for _, mode := range []string{"check", "copy", "sync"} {
		for _, changed := range []string{"source", "destination"} {
			t.Run(mode+"/"+changed, func(t *testing.T) {
				s, d := endpoints(t)
				write(t, s, "file", "OLD!")
				write(t, d, "file", "OLD!")
				name := s.At("file")
				if changed == "destination" {
					name = d.At("file")
				}
				d.FS = &comparisonMutation{onList: true, change: func() { replaceSameMetadata(t, name) }}
				o := opts()
				o.SizeOnly = true
				var err error
				if mode == "check" {
					err = Check(context.Background(), s, d, o)
				} else {
					_, err = BuildPlan(context.Background(), s, d, mode, o)
				}
				if err == nil || !strings.Contains(err.Error(), "changed after planning") {
					t.Fatalf("size-only accepted stale inventory: %v", err)
				}
			})
		}
	}
}

func TestCachedComparisonRechecksAfterProbe(t *testing.T) {
	for _, mode := range []string{"healthy", "source-identity", "target-identity", "target-mtime"} {
		t.Run(mode, func(t *testing.T) {
			cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("OLD!")}, id: 101}
			s := cloud.endpoint(7, "/Temp/a")
			d := Endpoint{&Local{}, filepath.Join(t.TempDir(), "file")}
			if err := os.WriteFile(d.Root, []byte("OLD!"), 0600); err != nil {
				t.Fatal(err)
			}
			entry, err := s.FS.Stat(context.Background(), s.Root)
			if err != nil {
				t.Fatal(err)
			}
			current, err := d.FS.Stat(context.Background(), d.Root)
			if err != nil {
				t.Fatal(err)
			}
			stage, err := stageFor(context.Background(), s.FS.(*Remote), s.Root, d.Root, entry)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := hashSHA256(context.Background(), d.Root)
			if err != nil {
				t.Fatal(err)
			}
			stage.info.Version, stage.info.ETag = completedDownloadVersion, `"fixture"`
			stage.info.LocalSize, stage.info.LocalModified = current.Size, current.ModTime.UnixNano()
			record, err := json.Marshal(completedDownload{Source: stage.info, SHA256: digest})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stage.record+".done", record, 0600); err != nil {
				t.Fatal(err)
			}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			probes := 0
			runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, _ string, _ int64) error {
				if kind != "probe" {
					t.Errorf("cache unexpectedly downloaded content: %s", kind)
				}
				probes++
				switch mode {
				case "source-identity":
					cloud.id++
				case "target-identity":
					replaceSameMetadata(t, d.Root)
				case "target-mtime":
					future := time.Now().Add(time.Hour)
					if err := os.Chtimes(d.Root, future, future); err != nil {
						return err
					}
				}
				reportProgress(ctx, transferProgress{Total: 4, ETag: `"fixture"`})
				return nil
			}
			plan, err := FilePlan(context.Background(), s, d, false, opts())
			if probes != 1 {
				t.Errorf("probe count=%d", probes)
			}
			if mode == "healthy" {
				if err != nil || plan == nil || plan.Actions[0].Kind != "skip" {
					t.Errorf("valid cached comparison failed: plan=%v err=%v", plan, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "changed after planning") || plan != nil {
				t.Errorf("probe-time change accepted: plan=%v err=%v", plan, err)
			}
		})
	}
}

func TestSkipActionsRecheckPlannedFiles(t *testing.T) {
	for _, mode := range []string{"copy", "sync", "move"} {
		for _, reason := range []string{"identical", "ignore-existing", "destination-newer"} {
			for _, changed := range []string{"healthy", "source", "destination"} {
				t.Run(mode+"/"+reason+"/"+changed, func(t *testing.T) {
					s, d := endpoints(t)
					write(t, s, "file", "OLD!")
					write(t, d, "file", "OLD!")
					if mode == "sync" {
						write(t, d, "extra", "keep if skip fails")
					}
					o := opts()
					o.IgnoreExisting = reason == "ignore-existing"
					o.Update = reason == "destination-newer"
					if o.Update {
						future := time.Now().Add(time.Hour)
						if err := os.Chtimes(d.At("file"), future, future); err != nil {
							t.Fatal(err)
						}
					}
					plan, err := BuildPlan(context.Background(), s, d, mode, o)
					if err != nil {
						t.Fatal(err)
					}
					if len(plan.Actions) == 0 || plan.Actions[0].Kind != "skip" || plan.Actions[0].Reason != reason {
						t.Fatalf("fixture did not plan expected skip: %+v", plan.Actions)
					}
					if changed == "source" {
						replaceSameMetadata(t, s.At("file"))
					}
					if changed == "destination" {
						replaceSameMetadata(t, d.At("file"))
					}
					result, err := Execute(context.Background(), plan, o)
					if changed == "healthy" {
						if err != nil || result.Skipped != 1 || result.Failed != 0 {
							t.Fatalf("healthy skip failed: %+v %v", result, err)
						}
						if mode == "move" && reason == "identical" {
							if _, err := os.Stat(s.At("file")); !os.IsNotExist(err) {
								t.Errorf("verified identical move retained source: %v", err)
							}
						} else if got, err := os.ReadFile(s.At("file")); err != nil || string(got) != "OLD!" {
							t.Errorf("healthy skip changed source: %q %v", got, err)
						}
						if got, err := os.ReadFile(d.At("file")); err != nil || string(got) != "OLD!" {
							t.Errorf("healthy skip changed target: %q %v", got, err)
						}
						if mode == "sync" && result.Deleted != 1 {
							t.Errorf("healthy sync did not delete extra file: %+v", result)
						}
					} else {
						if err == nil || !strings.Contains(err.Error(), "changed after planning") || result != (Result{Failed: 1}) {
							t.Errorf("stale skip accepted: %+v err=%v", result, err)
						}
						wantSource, wantTarget := "OLD!", "OLD!"
						if changed == "source" {
							wantSource = "NEW!"
						} else {
							wantTarget = "NEW!"
						}
						for name, want := range map[string]string{s.At("file"): wantSource, d.At("file"): wantTarget} {
							if got, err := os.ReadFile(name); err != nil || string(got) != want {
								t.Errorf("stale skip changed file: %q %v", got, err)
							}
						}
						if mode == "sync" {
							if got, err := os.ReadFile(d.At("extra")); err != nil || string(got) != "keep if skip fails" {
								t.Errorf("stale skip allowed deletion: %q %v", got, err)
							}
						}
					}
				})
			}
		}
	}
}
