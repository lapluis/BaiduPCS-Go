package rc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsDistinctUnicodeNamesTransfer(t *testing.T) {
	for _, mode := range []string{"copy", "sync", "move"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, s, "k.txt", "Latin k")
			write(t, s, "K.txt", "Kelvin sign")
			o := opts()
			o.Checksum = true
			plan, err := BuildPlan(context.Background(), s, d, mode, o)
			if err != nil {
				t.Fatal("distinct Windows names rejected:", err)
			}
			o.DryRun = true
			if _, err := Execute(context.Background(), plan, o); err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"k.txt": "Latin k", "K.txt": "Kelvin sign"} {
				if data, err := os.ReadFile(s.At(name)); err != nil || string(data) != want {
					t.Fatalf("preview changed source %s: %q %v", name, data, err)
				}
				if _, err := os.Stat(d.At(name)); !os.IsNotExist(err) {
					t.Fatalf("preview created target %s: %v", name, err)
				}
			}
			o.DryRun = false
			result, err := Execute(context.Background(), plan, o)
			if err != nil || result.Copied != 2 {
				t.Fatalf("distinct-name transfer failed: %+v %v", result, err)
			}
			for name, want := range map[string]string{"k.txt": "Latin k", "K.txt": "Kelvin sign"} {
				if data, err := os.ReadFile(d.At(name)); err != nil || string(data) != want {
					t.Fatalf("target name/content changed %s: %q %v", name, data, err)
				}
				if mode == "move" {
					if _, err := os.Stat(s.At(name)); !os.IsNotExist(err) {
						t.Errorf("move retained source %s: %v", name, err)
					}
				} else if data, err := os.ReadFile(s.At(name)); err != nil || string(data) != want {
					t.Errorf("copy/sync changed source %s: %q %v", name, data, err)
				}
			}
		})
	}
}

func TestWindowsDistinctUnicodeFileEndpoints(t *testing.T) {
	for _, move := range []bool{false, true} {
		root := t.TempDir()
		s := Endpoint{FS: &Local{}, Root: filepath.Join(root, "k.txt")}
		d := Endpoint{FS: &Local{}, Root: filepath.Join(root, "K.txt")}
		if err := os.WriteFile(s.Root, []byte("Latin k"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d.Root, []byte("Kelvin sign"), 0600); err != nil {
			t.Fatal(err)
		}
		plan, err := FilePlan(context.Background(), s, d, move, opts())
		if err != nil {
			t.Fatal("distinct endpoints treated as overlapping:", err)
		}
		if _, err := Execute(context.Background(), plan, opts()); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(d.Root); err != nil || string(data) != "Latin k" {
			t.Fatalf("incorrect target: %q %v", data, err)
		}
		if move {
			if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
				t.Fatalf("verified move retained source: %v", err)
			}
		} else if data, err := os.ReadFile(s.Root); err != nil || string(data) != "Latin k" {
			t.Fatalf("copy changed source: %q %v", data, err)
		}
	}
}

func TestWindowsUnicodeExcludedDestinationStaysDistinct(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "k.txt", "selected")
	write(t, d, "K.txt", "excluded data")
	o := opts()
	o.Filter, _ = NewFilter([]string{"k.txt"}, []string{"K.txt"}, "")
	plan, err := BuildPlan(context.Background(), s, d, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), plan, o); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"k.txt": "selected", "K.txt": "excluded data"} {
		if data, err := os.ReadFile(d.At(name)); err != nil || string(data) != want {
			t.Errorf("distinct target changed: name=%s data=%q err=%v", name, data, err)
		}
	}
}

func TestWindowsNativeCaseAliasesStillRejected(t *testing.T) {
	for _, pair := range [][2]string{{"a.txt", "A.TXT"}, {"ä.txt", "Ä.txt"}, {"σ.txt", "Σ.txt"}} {
		err := validateSelectedLocalPaths(map[string]Entry{pair[0]: {}, pair[1]: {}}, nil, opts())
		if err == nil || !strings.Contains(err.Error(), "case collision") {
			t.Errorf("native case alias accepted: %v err=%v", pair, err)
		}
	}
}

func TestWindowsPathOverlapUsesNativeOrdinalComparison(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"C:/parent/k", "c:/parent/K", false},
		{"C:/parent/k", "C:/parent/k/child", true},
		{"C:/a", "C:/ab", false},
		{"//server/share/a", "//SERVER/SHARE/A/sub", true},
		{"C:/Σ", "c:/σ/child", true},
		{"C:/ς", "C:/σ/child", false},
	} {
		got, err := localPathsOverlap(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("overlap(%q,%q)=%t,%v want %t", tc.a, tc.b, got, err, tc.want)
		}
	}
}
