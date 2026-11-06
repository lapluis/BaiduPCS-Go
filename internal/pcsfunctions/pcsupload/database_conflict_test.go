package pcsupload

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestUploadDatabaseStaleInstanceCannotOverwriteProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	first, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	meta := &checksum.LocalFileMeta{Path: "synthetic", Length: 8, ModTime: -1}
	first.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "newer-progress"})
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "stale-progress"})
	if err := second.Save(); !errors.Is(err, ErrCheckpointConflict) || !errors.Is(err, ErrCheckpointSave) {
		t.Errorf("stale instance conflict not reported: %v", err)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Error("conflicting save changed current disk progress")
	}
}

func TestUploadDatabaseSaveLeaseAndConflictRecovery(t *testing.T) {
	for _, firstSucceeds := range []bool{true, false} {
		t.Run(fmt.Sprint(firstSucceeds), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume.json")
			first, err := NewUploadingDatabaseAt(path)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := NewUploadingDatabaseAt(path)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			first.openFile = func(path string, flags int, mode os.FileMode) (*os.File, error) {
				close(entered)
				<-release
				if !firstSucceeds {
					return nil, os.ErrPermission
				}
				return os.OpenFile(path, flags, mode)
			}
			done := make(chan error, 1)
			go func() { done <- first.Save() }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("save did not acquire lease")
			}
			unexpected, openErr := NewUploadingDatabaseAt(path)
			if openErr == nil {
				unexpected.Close()
				t.Error("constructor read while a save held the lease")
			}
			if err := second.Save(); !errors.Is(err, ErrCheckpointSave) {
				t.Errorf("second writer was not excluded: %v", err)
			}
			unblock()
			err = <-done
			if firstSucceeds && err != nil || !firstSucceeds && !errors.Is(err, os.ErrPermission) {
				t.Errorf("unexpected first save: %v", err)
			}
			err = second.Save()
			if firstSucceeds && !errors.Is(err, ErrCheckpointConflict) || !firstSucceeds && err != nil {
				t.Errorf("wrong retry after lease release: %v", err)
			}
			fresh, err := NewUploadingDatabaseAt(path)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			if err := fresh.Save(); err != nil {
				t.Fatal("fresh snapshot could not save:", err)
			}
		})
	}
}

func TestUploadDatabaseExternalEditConflictsDespiteMatchingMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	db, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta := &checksum.LocalFileMeta{Path: "synthetic", Length: 8, ModTime: -1}
	db.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "AAAA"})
	if err := db.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(before), "AAAA", "BBBB", 1)
	if len(changed) != len(before) || changed == string(before) {
		t.Fatal("invalid fixture edit")
	}
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := db.Save(); !errors.Is(err, ErrCheckpointConflict) {
			t.Errorf("same-size/time external edit accepted: %v", err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != changed {
		t.Fatal("conflict rewrote external progress")
	}
	fresh, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if state := fresh.Search(meta); state == nil || state.Uploadid != "BBBB" {
		t.Fatal("fresh snapshot missed external progress")
	}
	if err := fresh.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadDatabaseFailedConstructorReleasesLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if db, err := NewUploadingDatabaseAt(path); err == nil || db != nil {
		t.Fatal("invalid snapshot accepted")
	}
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal("failed constructor retained lease:", err)
	}
	defer db.Close()
	if err := db.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadDatabaseInvalidPathsDoNotCreateLease(t *testing.T) {
	if _, err := NewUploadingDatabaseAt(""); err == nil {
		t.Fatal("empty path accepted")
	}
	dir := filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewUploadingDatabaseAt(dir); err == nil {
		t.Fatal("directory accepted as database")
	}
	if _, err := os.Stat(dir + ".lock"); !os.IsNotExist(err) {
		t.Fatal("invalid database path created a lease")
	}
}

func TestUploadDatabaseCrossProcessConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	first, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadDatabaseConflictProcessFixture$")
	cmd.Env = append(os.Environ(), "PCS_UPLOAD_CONFLICT_FIXTURE="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			cmd.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child not ready: %q %v", line, err)
	}
	meta := &checksum.LocalFileMeta{Path: "synthetic", Length: 8, ModTime: -1}
	first.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "parent-progress"})
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(stdin, "save"); err != nil {
		t.Fatal(err)
	}
	line, err = reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "conflict" {
		t.Fatalf("child conflict lost: %q %v", line, err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(before) {
		t.Fatal("child overwrote committed parent state")
	}
}

func TestUploadDatabaseConflictProcessFixture(t *testing.T) {
	path := os.Getenv("PCS_UPLOAD_CONFLICT_FIXTURE")
	if path == "" {
		return
	}
	db, err := NewUploadingDatabaseAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	db.UpdateUploading(&checksum.LocalFileMeta{Path: "synthetic", Length: 8, ModTime: -1}, &uploader.InstanceState{Uploadid: "stale-child"})
	if err := db.Save(); !errors.Is(err, ErrCheckpointConflict) || !errors.Is(err, ErrCheckpointSave) {
		t.Fatalf("child overwrote newer state: %v", err)
	}
	fmt.Println("conflict")
}

func TestUploadDatabaseWindowsAliasConflict(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows aliases")
	}
	for _, kind := range []string{"case", "trailing-dot"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume.json")
			first, err := NewUploadingDatabaseAt(path)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			alias := strings.ToUpper(path)
			if kind == "trailing-dot" {
				alias = path + "."
			}
			second, err := NewUploadingDatabaseAt(alias)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			if first.filename != second.filename {
				t.Fatalf("aliases use different lock names: %q %q", first.filename, second.filename)
			}
			if err := first.Save(); err != nil {
				t.Fatal(err)
			}
			if err := second.Save(); !errors.Is(err, ErrCheckpointConflict) {
				t.Fatalf("alias save bypassed conflict check: %v", err)
			}
		})
	}
}
