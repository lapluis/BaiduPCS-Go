package rc

import (
	"context"
	"os"
	"testing"
)

func replaceSameMetadata(t *testing.T, p string) {
	t.Helper()
	old, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p+".replacement", []byte("NEW!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(p+".replacement", old.ModTime(), old.ModTime()); err != nil {
		t.Fatal(err)
	}
	// Keep the old inode alive so replacement identity cannot be recycled.
	if err = os.Rename(p, p+".previous"); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(p+".replacement", p); err != nil {
		t.Fatal(err)
	}
}
func TestLocalReplacementWithSameMetadataIsChanged(t *testing.T) {
	s, _ := endpoints(t)
	write(t, s, "file", "OLD!")
	old, err := s.FS.Stat(context.Background(), s.At("file"))
	if err != nil {
		t.Fatal(err)
	}
	replaceSameMetadata(t, s.At("file"))
	if err = unchanged(context.Background(), s, "file", &old); err == nil {
		t.Fatal("replacement accepted as unchanged")
	}
}
func TestSyncRetainsReplacedDeletionCandidate(t *testing.T) {
	s, d := endpoints(t)
	write(t, d, "extra", "OLD!")
	plan, err := BuildPlan(context.Background(), s, d, "sync", opts())
	if err != nil {
		t.Fatal(err)
	}
	replaceSameMetadata(t, d.At("extra"))
	if _, err = Execute(context.Background(), plan, opts()); err == nil {
		t.Fatal("replacement deleted without error")
	}
	data, err := os.ReadFile(d.At("extra"))
	if err != nil || string(data) != "NEW!" {
		t.Fatal(string(data), err)
	}
}

func TestSourceIdentityChangeBlocksSyncDeletion(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "file", "OLD!")
	write(t, d, "file", "OLD!")
	write(t, d, "extra", "keep")
	plan, err := BuildPlan(context.Background(), s, d, "sync", opts())
	if err != nil {
		t.Fatal(err)
	}
	replaceSameMetadata(t, s.At("file"))
	if err = os.Remove(s.At("file.previous")); err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(context.Background(), plan, opts()); err == nil {
		t.Fatal("source replacement not detected")
	}
	if _, err = os.Stat(d.At("extra")); err != nil {
		t.Fatal("deleted despite source identity change", err)
	}
}

func TestMoveRetainsSourceOnSameMetadataTargetReplacement(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "file", "OLD!")
	write(t, d, "file", "OLD!")
	entry, err := s.FS.Stat(context.Background(), s.At("file"))
	if err != nil {
		t.Fatal(err)
	}
	d.FS = &changedVerificationTarget{change: func(p string) error { replaceSameMetadata(t, p); return nil }}
	if err = verifyAndRemove(context.Background(), s, d, Action{Path: "file", Source: &entry}, opts()); err == nil {
		t.Fatal("replacement accepted")
	}
	if _, err = os.Stat(s.At("file")); err != nil {
		t.Fatal("source deleted", err)
	}
}
