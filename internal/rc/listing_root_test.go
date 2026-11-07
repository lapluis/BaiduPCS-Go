package rc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertListingRejectsLinkedRoot(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	if _, err := (&Local{}).Stat(ctx, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("fixture is not rejected by existing metadata validation: %v", err)
	}
	if entries, err := (&Local{}).List(ctx, root); err == nil || entries != nil {
		t.Errorf("local listing followed linked root: entries=%v err=%v", entries, err)
	}
	for _, recursive := range []bool{false, true} {
		if entries, err := Listing(ctx, Endpoint{&Local{}, root}, recursive, -1, Filter{}); err == nil || entries != nil {
			t.Errorf("inventory followed linked root: recursive=%t entries=%v err=%v", recursive, entries, err)
		}
	}
	for _, command := range []string{"lsjson", "size", "tree"} {
		p, err := parse([]string{root})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err = runCommand(ctx, command, p, &Resolver{CWD: filepath.Dir(root)}, &output)
		if err == nil || output.Len() != 0 {
			t.Errorf("command followed linked root: command=%s output=%q err=%v", command, output.String(), err)
		}
	}
}

func TestLocalListingRejectsRootSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	assertListingRejectsLinkedRoot(t, link)
}
