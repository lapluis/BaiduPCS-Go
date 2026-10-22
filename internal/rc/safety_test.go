package rc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSourceAddedAfterPlanningBlocksDelete(t *testing.T) {
	s, d := endpoints(t)
	write(t, d, "new", "x")
	p, err := BuildPlan(context.Background(), s, d, "sync", opts())
	if err != nil {
		t.Fatal(err)
	}
	write(t, s, "new", "x")
	if _, err = Execute(context.Background(), p, opts()); err == nil {
		t.Fatal("source change ignored")
	}
	if _, err = os.Stat(d.At("new")); err != nil {
		t.Fatal("destination deleted")
	}
}
func TestDeleteExcludedEvenWhenSourceExists(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "secret.tmp", "source")
	write(t, d, "secret.tmp", "target")
	o := opts()
	o.Filter, _ = NewFilter(nil, []string{"*.tmp"}, "")
	o.DeleteExcluded = true
	p, err := BuildPlan(ctx, s, d, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, o); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(d.At("secret.tmp")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.At("secret.tmp")); err != nil {
		t.Fatal(err)
	}
}
func TestDestinationAppearedAfterPlan(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "source")
	p, err := BuildPlan(ctx, s, d, "copy", opts())
	if err != nil {
		t.Fatal(err)
	}
	write(t, d, "a", "new target")
	if _, err = Execute(ctx, p, opts()); err == nil {
		t.Fatal("new destination overwritten")
	}
	b, _ := os.ReadFile(d.At("a"))
	if string(b) != "new target" {
		t.Fatal(string(b))
	}
}
func TestSyncRemovesExtraEmptyDirectories(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	os.MkdirAll(d.At("extra/nested"), 0755)
	p, err := BuildPlan(ctx, s, d, "sync", opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, opts()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(d.At("extra")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestIgnoreExistingMoveRetainsSource(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "source")
	write(t, d, "a", "target")
	o := opts()
	o.IgnoreExisting = true
	p, err := BuildPlan(ctx, s, d, "move", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, o); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.At("a")); err != nil {
		t.Fatal(err)
	}
}
func TestUpdateUsesDestinationTime(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "source")
	write(t, d, "a", "newer")
	future := time.Now().Add(time.Hour)
	os.Chtimes(d.At("a"), future, future)
	o := opts()
	o.Update = true
	p, err := BuildPlan(ctx, s, d, "copy", o)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Reason != "destination-newer" {
		t.Fatal(p.Actions)
	}
}
func TestEmptyDirectoriesAndEmptyFile(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	os.MkdirAll(s.At("empty/child"), 0755)
	write(t, s, "zero", "")
	o := opts()
	o.CreateEmptyDirs = true
	p, err := BuildPlan(ctx, s, d, "copy", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, o); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(d.At("empty/child")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(d.At("zero"))
	if err != nil || st.Size() != 0 {
		t.Fatal(st, err)
	}
}
func TestRmdirsDryRunIncludesParents(t *testing.T) {
	ctx := context.Background()
	_, d := endpoints(t)
	os.MkdirAll(d.At("a/b"), 0755)
	o := opts()
	o.DryRun = true
	var paths []string
	o.Emit = func(_, p string) { paths = append(paths, p) }
	if err := Delete(ctx, d, "rmdirs", o); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatal(paths)
	}
	if _, err := os.Stat(d.At("a/b")); err != nil {
		t.Fatal(err)
	}
}
func TestParserAndApplicability(t *testing.T) {
	p, err := parse([]string{"a", "--transfers", "2", "b", "--dry-run", "--include=*.txt", "-P"})
	if err != nil || len(p.Args) != 2 || p.Transfers != 2 || !p.DryRun || !p.Progress {
		t.Fatal(p, err)
	}
	if err = validateFlags("purge", p); err == nil {
		t.Fatal("purge silently accepted filters")
	}
	for _, args := range [][]string{{"--transfers", "0"}, {"--checksum", "--size-only"}, {"--bwlimit", "NaN"}, {"--bogus"}, {"--files-only", "--dirs-only"}} {
		if _, err = parse(args); err == nil {
			t.Fatal(args)
		}
	}
}
func TestListJSONCleanAndRelativePaths(t *testing.T) {
	ctx := context.Background()
	s, _ := endpoints(t)
	write(t, s, "中文.txt", "abc")
	p, _ := parse([]string{s.Root})
	r := &Resolver{CWD: s.Root, Aliases: map[string]uint64{}}
	var out bytes.Buffer
	if err := runCommand(ctx, "lsjson", p, r, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "[") || !strings.Contains(out.String(), "中文.txt") {
		t.Fatal(out.String())
	}
	e, err := r.Resolve("sub")
	if err != nil || e.Root != filepath.Join(s.Root, "sub") {
		t.Fatal(e, err)
	}
}
func TestSymlinkAndCaseCollisions(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "a")
	if err := os.Symlink(s.At("a"), s.At("link")); err == nil {
		if _, err = BuildPlan(ctx, s, d, "copy", opts()); err == nil {
			t.Fatal("symlink accepted")
		}
		os.Remove(s.At("link"))
	}
	if runtime.GOOS == "windows" {
		write(t, d, "A", "a")
		if _, err := BuildPlan(ctx, s, d, "copy", opts()); err == nil {
			t.Fatal("case conflict accepted")
		}
	}
}

type disappearedChild struct{ *Local }

func (disappearedChild) List(context.Context, string) ([]Entry, error) { return nil, os.ErrNotExist }
func TestMissingChildIsNotAnEmptyDestination(t *testing.T) {
	s, d := endpoints(t)
	d.FS = disappearedChild{&Local{}}
	_, err := BuildPlan(context.Background(), s, d, "sync", opts())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestDirectoryExclusionAppliesToDescendants(t *testing.T) {
	f, err := NewFilter(nil, []string{"secret/"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.Match("secret/nested/a.txt") || f.Match("secret") {
		t.Fatal("excluded subtree selected")
	}
	if !f.Match("public/a.txt") {
		t.Fatal("unrelated file excluded")
	}
}
func TestEmptySourceCreatesTargetRoot(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	d.Root = d.At("new")
	p, err := BuildPlan(ctx, s, d, "copy", opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, opts()); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(d.Root); err != nil || !st.IsDir() {
		t.Fatal(st, err)
	}
}
