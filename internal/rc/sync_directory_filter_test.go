package rc

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSyncDirectoryExclusionsDeletionLimitsAndPreview(t *testing.T) {
	for _, dry := range []bool{false, true} {
		for _, deleteExcluded := range []bool{false, true} {
			for _, limit := range []int{2, 3} {
				t.Run(fmt.Sprintf("dry=%t/delete-excluded=%t/limit=%d", dry, deleteExcluded, limit), func(t *testing.T) {
					source, target := endpoints(t)
					write(t, source, "new.txt", "new content")
					original := map[string]string{
						"secret":                 "ordinary same-named file",
						"parent/ordinary.txt":    "ordinary nested file",
						"parent/secret/keep.txt": "protected content",
					}
					for name, content := range original {
						write(t, target, name, content)
					}
					if err := os.MkdirAll(target.At("parent/secret/empty"), 0700); err != nil {
						t.Fatal(err)
					}
					o := opts()
					o.DryRun, o.DeleteExcluded, o.MaxDelete = dry, deleteExcluded, limit
					var err error
					o.Filter, err = NewFilter(nil, []string{"**/secret/"}, "")
					if err != nil {
						t.Fatal(err)
					}
					deletions, directories := map[string]bool{}, map[string]bool{}
					o.Emit = func(kind, name string) {
						prefix := ""
						if dry {
							prefix = "would-"
							if kind == "delete" || kind == "rmdir" || kind == "copy" {
								t.Errorf("preview emitted actual action: %s %s", kind, name)
							}
						}
						switch kind {
						case prefix + "delete":
							deletions[name] = true
						case prefix + "rmdir":
							directories[name] = true
						}
					}
					blocked := deleteExcluded && limit == 2
					plan, err := BuildPlan(context.Background(), source, target, "sync", o)
					if blocked {
						if err == nil || !strings.Contains(err.Error(), "planned deletions 3 exceed --max-delete 2") || plan != nil {
							t.Fatalf("deletion limit did not block whole plan: plan=%+v err=%v", plan, err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if _, err := Execute(context.Background(), plan, o); err != nil {
							t.Fatal(err)
						}
						wantFiles := map[string]bool{"secret": true, "parent/ordinary.txt": true}
						wantDirs := map[string]bool{}
						if deleteExcluded {
							wantFiles["parent/secret/keep.txt"] = true
							for _, name := range []string{"parent/secret/empty", "parent/secret", "parent"} {
								wantDirs[name] = true
							}
						}
						if !reflect.DeepEqual(deletions, wantFiles) || !reflect.DeepEqual(directories, wantDirs) {
							t.Errorf("wrong removal events: files=%v dirs=%v want files=%v dirs=%v", deletions, directories, wantFiles, wantDirs)
						}
					}
					for name, want := range original {
						data, err := os.ReadFile(target.At(name))
						retained := blocked || dry || name == "parent/secret/keep.txt" && !deleteExcluded
						if retained {
							if err != nil || string(data) != want {
								t.Errorf("retained file changed: %s data=%q err=%v", name, data, err)
							}
						} else if !os.IsNotExist(err) {
							t.Errorf("selected file retained: %s err=%v", name, err)
						}
					}
					for _, name := range []string{"parent", "parent/secret", "parent/secret/empty"} {
						info, err := os.Stat(target.At(name))
						if blocked || dry || !deleteExcluded {
							if err != nil || !info.IsDir() {
								t.Errorf("protected/preview directory changed: %s err=%v", name, err)
							}
						} else if !os.IsNotExist(err) {
							t.Errorf("extra empty directory retained: %s err=%v", name, err)
						}
					}
					data, err := os.ReadFile(target.At("new.txt"))
					if blocked || dry {
						if !os.IsNotExist(err) {
							t.Errorf("blocked/preview copy wrote target: %v", err)
						}
					} else if err != nil || string(data) != "new content" {
						t.Errorf("new copy missing: data=%q err=%v", data, err)
					}
					if data, err := os.ReadFile(source.At("new.txt")); err != nil || string(data) != "new content" {
						t.Errorf("sync changed source: data=%q err=%v", data, err)
					}
				})
			}
		}
	}
}
