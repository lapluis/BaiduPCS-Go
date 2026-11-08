package rc

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
)

type sizeInventoryBackend struct {
	Backend
	entries []Entry
}

func (*sizeInventoryBackend) Identity() string { return "size-fixture" }
func (*sizeInventoryBackend) Stat(context.Context, string) (Entry, error) {
	return Entry{IsDir: true}, nil
}
func (b *sizeInventoryBackend) List(context.Context, string) ([]Entry, error) { return b.entries, nil }

func TestTransferPlanRejectsByteTotalOverflow(t *testing.T) {
	for _, mode := range []string{"copy", "sync", "move"} {
		for _, exclude := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exclude=%t", mode, exclude), func(t *testing.T) {
				_, dst := endpoints(t)
				write(t, dst, "only-copy", "retained")
				src := Endpoint{FS: &sizeInventoryBackend{entries: []Entry{{Name: "a", Size: math.MaxInt64}, {Name: "b", Size: 1}}}, Root: "/source"}
				o := opts()
				if exclude {
					var err error
					o.Filter, err = NewFilter(nil, []string{"b"}, "")
					if err != nil {
						t.Fatal(err)
					}
				}
				plan, err := BuildPlan(context.Background(), src, dst, mode, o)
				if exclude {
					if err != nil || plan == nil {
						t.Fatalf("filtered valid total rejected: %v", err)
					}
					if got := newProgress(plan).snapshot(Result{}, 0).Total; got != math.MaxInt64 {
						t.Errorf("total=%d, want MaxInt64", got)
					}
				} else if err == nil || plan != nil {
					t.Errorf("overflow authorized a plan: plan=%v err=%v", plan != nil, err)
				}
				if data, err := os.ReadFile(dst.At("only-copy")); err != nil || string(data) != "retained" {
					t.Fatalf("destination changed: %q, %v", data, err)
				}
			})
		}
	}
}

func TestExecuteRejectsByteOverflowBeforePreparation(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			src, dst := endpoints(t)
			plan := &Plan{Source: src, Destination: dst, Actions: []Action{
				{Kind: "mkdir", Path: "new-directory"},
				{Kind: "skip", Path: "a", Source: &Entry{Size: math.MaxInt64}},
				{Kind: "skip", Path: "b", Source: &Entry{Size: 1}},
			}}
			o := opts()
			o.DryRun = dry
			logs := 0
			o.Emit = func(string, string) { logs++ }
			_, err := Execute(context.Background(), plan, o)
			if err == nil || !strings.Contains(err.Error(), "total file size") {
				t.Errorf("overflow not rejected before execution: %v", err)
			}
			if _, err := os.Stat(dst.At("new-directory")); !os.IsNotExist(err) {
				t.Errorf("preparation ran: %v", err)
			}
			if logs != 0 {
				t.Errorf("invalid plan emitted %d execution records", logs)
			}
		})
	}
}

func TestLegacySizeRejectsOverflow(t *testing.T) {
	for _, cmd := range []string{"size", "lsjson"} {
		t.Run(cmd, func(t *testing.T) {
			r, out := quotaCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":1,"path":"/a","size":9223372036854775807},{"fs_id":2,"path":"/b","size":1}]}`)
			})
			err := runCommand(context.Background(), cmd, parsed{Args: []string{"bd:"}}, r, out)
			if cmd == "size" {
				if err == nil || out.Len() != 0 {
					t.Fatalf("overflow reported success: err=%v output=%q", err, out.String())
				}
				return
			}
			var entries []Entry
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out.Bytes(), &entries); err != nil || len(entries) != 2 {
				t.Fatalf("listing individual sizes failed: %q, %v", out.String(), err)
			}
		})
	}
}

func TestInventorySizeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		entries      []Entry
		count, total int64
		invalid      bool
	}{
		{"empty", nil, 0, 0, false},
		{"ordinary", []Entry{{Size: 3}, {Size: 4}, {IsDir: true, Size: math.MaxInt64}}, 2, 7, false},
		{"exact-limit", []Entry{{Size: math.MaxInt64 - 1}, {Size: 1}}, 2, math.MaxInt64, false},
		{"one-byte-over", []Entry{{Size: math.MaxInt64}, {Size: 1}}, 0, 0, true},
		{"two-maxima", []Entry{{Size: math.MaxInt64}, {Size: math.MaxInt64}}, 0, 0, true},
		{"negative", []Entry{{Size: 3}, {Size: -1}}, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count, total, err := InventorySize(tc.entries)
			if (err != nil) != tc.invalid || count != tc.count || total != tc.total {
				t.Fatalf("count=%d total=%d err=%v; want count=%d total=%d invalid=%t", count, total, err, tc.count, tc.total, tc.invalid)
			}
		})
	}
}
