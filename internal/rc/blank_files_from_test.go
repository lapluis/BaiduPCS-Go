package rc

import "testing"

func TestLegacyRejectsBlankFilesFrom(t *testing.T) {
	for _, flags := range [][]string{{"--files-from", ""}, {"--files-from="}, {"--files-from", " \t "}, {"--files-from= \t"}} {
		if _, err := parse(flags); err == nil {
			t.Errorf("blank file selection accepted: %q", flags)
		}
	}
	if p, err := parse(nil); err != nil || p.FilesFrom != "" || p.Seen["files-from"] {
		t.Fatalf("omitted file selection changed: %+v err=%v", p, err)
	}
	const path = "selection with spaces.txt"
	if p, err := parse([]string{"--files-from", path}); err != nil || p.FilesFrom != path {
		t.Fatalf("valid file selection changed: %+v err=%v", p, err)
	}
}
