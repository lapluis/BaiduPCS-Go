package rc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func endpoints(t *testing.T) (Endpoint, Endpoint) {
	t.Helper()
	return Endpoint{&Local{}, t.TempDir()}, Endpoint{&Local{}, t.TempDir()}
}
func write(t *testing.T, e Endpoint, p, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(e.At(p)), 0755)
	if err := os.WriteFile(e.At(p), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func opts() Options { return Options{Transfers: 2, Checkers: 2, MaxDelete: -1} }
func TestCopySyncDryRun(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "sub/a", "new")
	write(t, d, "extra", "keep")
	write(t, d, "sub/a", "old")
	o := opts()
	p, err := BuildPlan(ctx, s, d, "copy", o)
	if err != nil {
		t.Fatal(err)
	}
	o.DryRun = true
	if _, err = Execute(ctx, p, o); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(d.At("sub/a"))
	if string(b) != "old" {
		t.Fatal("dry run wrote")
	}
	o.DryRun = false
	if _, err = Execute(ctx, p, o); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(d.At("extra")); err != nil {
		t.Fatal("copy deleted extra")
	}
	p, err = BuildPlan(ctx, s, d, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Execute(ctx, p, o)
	if err != nil || r.Copied != 0 || r.Skipped != 1 || r.Deleted != 1 {
		t.Fatal(r, err)
	}
}
func TestDeletionLimitAndFilters(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, d, "extra", "x")
	write(t, d, "keep.tmp", "x")
	o := opts()
	o.MaxDelete = 0
	if _, err := BuildPlan(ctx, s, d, "sync", o); err == nil {
		t.Fatal("limit ignored")
	}
	o.MaxDelete = 1
	o.Filter, _ = NewFilter(nil, []string{"*.tmp"}, "")
	p, err := BuildPlan(ctx, s, d, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, o); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(d.At("keep.tmp")); err != nil {
		t.Fatal("excluded file deleted")
	}
}
func TestChangesAfterPlanBlockDeletion(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "source")
	write(t, d, "extra", "x")
	o := opts()
	p, err := BuildPlan(ctx, s, d, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	write(t, s, "a", "changed source")
	if _, err = Execute(ctx, p, o); err == nil {
		t.Fatal("change ignored")
	}
	if _, err = os.Stat(d.At("extra")); err != nil {
		t.Fatal("deleted after transfer error")
	}
}
func TestMoveNeverRemovesUnequalSizeOnlySkip(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "abc")
	write(t, d, "a", "xyz")
	o := opts()
	o.SizeOnly = true
	p, err := BuildPlan(ctx, s, d, "move", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, o); err == nil {
		t.Fatal("unverified move succeeded")
	}
	if _, err = os.Stat(s.At("a")); err != nil {
		t.Fatal("lost source")
	}
}
func TestMoveAndExactTarget(t *testing.T) {
	ctx := context.Background()
	s, d := endpoints(t)
	write(t, s, "a", "abc")
	s.Root = s.At("a")
	d.Root = d.At("renamed")
	p, err := FilePlan(ctx, s, d, true, opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(ctx, p, opts()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("source remains")
	}
	b, _ := os.ReadFile(d.Root)
	if string(b) != "abc" {
		t.Fatal(string(b))
	}
}

type failedList struct{ *Local }

func (failedList) List(context.Context, string) ([]Entry, error) {
	return nil, errors.New("injected scan failure")
}
func TestScanFailureAndOverlap(t *testing.T) {
	s, d := endpoints(t)
	s.FS = failedList{&Local{}}
	if _, err := BuildPlan(context.Background(), s, d, "sync", opts()); err == nil {
		t.Fatal("scan failure ignored")
	}
	s.FS = &Local{}
	d.Root = filepath.Join(s.Root, "child")
	if _, err := BuildPlan(context.Background(), s, d, "sync", opts()); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatal(err)
	}
}
func TestDeleteAndPurge(t *testing.T) {
	ctx := context.Background()
	_, d := endpoints(t)
	write(t, d, "nested/a", "a")
	if err := Delete(ctx, d, "rmdir", opts()); err == nil {
		t.Fatal("nonempty rmdir accepted")
	}
	o := opts()
	o.DryRun = true
	if err := Delete(ctx, d, "purge", o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.At("nested/a")); err != nil {
		t.Fatal(err)
	}
	o.DryRun = false
	if err := Delete(ctx, d, "purge", o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Root); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, d := endpoints(t)
	write(t, s, "a", "a")
	p, err := BuildPlan(ctx, s, d, "copy", opts())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = Execute(ctx, p, opts()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
