package pcsconfig

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSaveRelativeLinkUsesPhysicalParent(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			root := t.TempDir()
			physicalParent := filepath.Join(root, "physical")
			physicalDir := filepath.Join(physicalParent, "nested")
			if err := os.MkdirAll(physicalDir, 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(root, "alias")
			if err := os.Symlink(physicalDir, alias); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
			if err := os.Symlink(filepath.Join("..", "actual.json"), filepath.Join(physicalDir, "settings.json")); err != nil {
				t.Fatal(err)
			}
			actual, unrelated := filepath.Join(physicalParent, "actual.json"), filepath.Join(root, "actual.json")
			canary, saved := []byte(`{"unrelated":true}`), []byte(`{"saved":true}`)
			if err := os.WriteFile(unrelated, canary, 0600); err != nil {
				t.Fatal(err)
			}
			if exists {
				if err := os.WriteFile(actual, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := filepath.Join(alias, "settings.json")
			resolved, err := ResolveConfigDestination(config)
			if err != nil || resolved != actual {
				t.Errorf("relative target resolved against logical parent: got=%q want=%q err=%v", resolved, actual, err)
			}
			if err := saveConfigData(config, saved); err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string][]byte{actual: saved, unrelated: canary} {
				if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
					t.Errorf("wrong configuration target written: %s data=%q err=%v", path, data, err)
				}
			}
			for _, link := range []string{alias, config} {
				if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("symbolic link replaced: %s info=%v err=%v", link, info, err)
				}
			}
		})
	}
}

func TestConfigRelativeTargetKeepsIntermediateLinkTraversal(t *testing.T) {
	root := t.TempDir()
	base, elsewhere := filepath.Join(root, "base"), filepath.Join(root, "elsewhere")
	for _, dir := range []string{base, filepath.Join(elsewhere, "deep")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(elsewhere, "deep"), filepath.Join(base, "sub")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	config := filepath.Join(base, "settings.json")
	if err := os.Symlink("sub/../actual.json", config); err != nil {
		t.Fatal(err)
	}
	actual, unrelated := filepath.Join(elsewhere, "actual.json"), filepath.Join(base, "actual.json")
	canary, saved := []byte(`{"unrelated":true}`), []byte(`{"saved":true}`)
	if err := os.WriteFile(unrelated, canary, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveConfigData(config, saved); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{actual: saved, unrelated: canary} {
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
			t.Errorf("intermediate link traversal changed: %s data=%q err=%v", path, data, err)
		}
	}
	// Repeat with a missing target parent; preflight must follow the same
	// traversal without creating the future directory or file.
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/../future/actual.json", config); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(elsewhere, "future", "actual.json")
	resolved, err := ResolveConfigDestination(config)
	if err != nil || resolved != want {
		t.Errorf("future target traversal changed: got=%q want=%q err=%v", resolved, want, err)
	}
	if _, err := os.Stat(filepath.Dir(want)); !os.IsNotExist(err) {
		t.Errorf("preflight created a future directory: %v", err)
	}
}
