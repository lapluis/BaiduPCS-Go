package pcsconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSaveCreatesPhysicalLinkTargetParents(t *testing.T) {
	for _, shape := range []string{"absolute", "relative", "linked-parent", "internal-link"} {
		for _, initialize := range []bool{false, true} {
			t.Run(shape+map[bool]string{false: "/data", true: "/initialize"}[initialize], func(t *testing.T) {
				root := t.TempDir()
				base := filepath.Join(root, "location", "nested")
				if err := os.MkdirAll(base, 0700); err != nil {
					t.Fatal(err)
				}
				actual := filepath.Join(root, "location", "shared", "future", "deep", "actual.json")
				canaries := []string{filepath.Join(root, "shared", "future", "deep", "actual.json"), filepath.Join(base, "future", "deep", "actual.json")}
				for _, name := range canaries {
					if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(name, []byte(`{"unrelated":true}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				config := filepath.Join(base, "settings.json")
				target := actual
				if shape != "absolute" {
					var err error
					target, err = filepath.Rel(base, actual)
					if err != nil {
						t.Fatal(err)
					}
				}
				if shape == "internal-link" {
					branch := filepath.Join(root, "location", "shared", "branch")
					if err := os.MkdirAll(branch, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(branch, filepath.Join(base, "redirect")); err != nil {
						t.Skipf("symbolic links unavailable: %v", err)
					}
					// Keep .. after the directory link; filepath.Join would clean it.
					target = "redirect/../future/deep/actual.json"
				}
				if err := os.Symlink(target, config); err != nil {
					t.Skipf("symbolic links unavailable: %v", err)
				}
				if shape == "linked-parent" {
					alias := filepath.Join(root, "alias")
					if err := os.Symlink(base, alias); err != nil {
						t.Fatal(err)
					}
					config = filepath.Join(alias, "settings.json")
				}
				resolved, err := ResolveConfigDestination(config)
				if err != nil || resolved != actual {
					t.Fatalf("future target resolved incorrectly: got=%q want=%q err=%v", resolved, actual, err)
				}
				if _, err := os.Stat(filepath.Dir(actual)); !os.IsNotExist(err) {
					t.Fatalf("preflight created target parents: %v", err)
				}
				if initialize {
					c := NewConfig(config)
					if err := c.Init(); err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					c.AppID = 77
					if err := c.Save(); err != nil {
						t.Fatal(err)
					}
					if err := c.Reload(); err != nil || c.AppID != 77 {
						t.Fatalf("initialized config did not reload: appid=%d err=%v", c.AppID, err)
					}
				} else if err := SaveConfigData(config, []byte(`{"appid":77}`)); err != nil {
					t.Fatal(err)
				}
				var value struct {
					AppID int `json:"appid"`
				}
				data, err := os.ReadFile(actual)
				if err != nil || json.Unmarshal(data, &value) != nil || value.AppID != 77 {
					t.Fatalf("physical target not saved: %q %v", data, err)
				}
				if got, err := os.Readlink(config); err != nil || got != target {
					t.Errorf("configuration link replaced: got=%q want=%q err=%v", got, target, err)
				}
				for _, name := range canaries {
					if data, err := os.ReadFile(name); err != nil || string(data) != `{"unrelated":true}` {
						t.Errorf("unrelated target modified: %q %v", data, err)
					}
				}
			})
		}
	}
}
