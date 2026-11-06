package pcsconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type faultConfigTemp struct {
	*os.File
	chmodErr, syncErr, closeErr error
	closes                      int
}

func (f *faultConfigTemp) Chmod(mode os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.File.Chmod(mode)
}

func (f *faultConfigTemp) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.File.Sync()
}

func (f *faultConfigTemp) Close() error {
	f.closes++
	return errors.Join(f.File.Close(), f.closeErr)
}

func TestConfigSaveFailurePreservesOriginal(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"create", "chmod", "write", "short", "sync", "close", "write-close", "sync-close", "replace"} {
			name := "new/" + mode
			if existing {
				name = "existing/" + mode
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "pcs.json")
				original := []byte(`{"user_agent":"original synthetic config"}`)
				if existing {
					if err := os.WriteFile(path, original, 0600); err != nil {
						t.Fatal(err)
					}
				}
				c := NewConfig(path)
				c.InitDefaultConfig()
				defer c.Close()
				failure := errors.New("fixture config " + mode + " failure")
				closeFailure := errors.New("fixture config secondary close failure")
				previousWrite, previousCreate, previousReplace := writeConfigData, createConfigTemp, replaceConfigFile
				defer func() {
					writeConfigData, createConfigTemp, replaceConfigFile = previousWrite, previousCreate, previousReplace
				}()
				var temp *faultConfigTemp
				createConfigTemp = func(dir string) (configTempFile, error) {
					if mode == "create" {
						return nil, failure
					}
					file, err := os.CreateTemp(dir, ".pcs-config-*")
					if err != nil {
						return nil, err
					}
					temp = &faultConfigTemp{File: file}
					if mode == "chmod" {
						temp.chmodErr = failure
					}
					if mode == "sync" || mode == "sync-close" {
						temp.syncErr = failure
					}
					if mode == "close" {
						temp.closeErr = failure
					}
					if strings.HasSuffix(mode, "-close") {
						temp.closeErr = closeFailure
					}
					return temp, nil
				}
				if mode == "write" || mode == "short" || mode == "write-close" {
					writeConfigData = func(w io.Writer, data []byte) (int, error) {
						n, err := w.Write(data[:len(data)/2])
						if err != nil {
							return n, err
						}
						if mode != "short" {
							return n, failure
						}
						return n, nil
					}
				}
				if mode == "replace" {
					replaceConfigFile = func(string, string) error { return failure }
				}
				err := c.Save()
				want := error(failure)
				if mode == "short" {
					want = io.ErrShortWrite
				}
				if !errors.Is(err, want) {
					t.Errorf("save failure lost: %v", err)
				}
				if strings.HasSuffix(mode, "-close") && !errors.Is(err, closeFailure) {
					t.Errorf("secondary close failure lost: %v", err)
				}
				data, readErr := os.ReadFile(path)
				if existing {
					if readErr != nil || !bytes.Equal(data, original) {
						t.Errorf("original config changed: bytes=%d err=%v", len(data), readErr)
					}
				} else if !os.IsNotExist(readErr) {
					t.Errorf("failed save published a config: bytes=%d err=%v", len(data), readErr)
				}
				if temp != nil && temp.closes != 1 {
					t.Errorf("temporary file closes=%d", temp.closes)
				}
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".pcs-config-") {
						t.Errorf("temporary config leaked: %s", entry.Name())
					}
				}
				writeConfigData, createConfigTemp, replaceConfigFile = previousWrite, previousCreate, previousReplace
				if err := c.Save(); err != nil {
					t.Fatalf("failed save prevented a later retry: %v", err)
				}
				data, err = os.ReadFile(path)
				var snapshot map[string]interface{}
				if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot["user_agent"] != c.UserAgent {
					t.Fatal("retry did not publish a complete config")
				}
			})
		}
	}
}

func TestConfigSaveReplacesCompleteSnapshotAndReloads(t *testing.T) {
	c := reloadFixture(t)
	if err := c.Init(); err != nil {
		t.Fatal(err)
	}
	c.UserAgent = strings.Repeat("synthetic", 100)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(c.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	c.UserAgent = "short synthetic value"
	previousReplace := replaceConfigFile
	defer func() { replaceConfigFile = previousReplace }()
	called := false
	replaceConfigFile = func(from, to string) error {
		called = true
		if filepath.Dir(from) != filepath.Dir(to) {
			t.Error("snapshot is on a different filesystem directory")
		}
		old, err := os.ReadFile(to)
		if err != nil || !bytes.Equal(old, before) {
			t.Error("old config changed before replacement")
		}
		data, err := os.ReadFile(from)
		var snapshot map[string]interface{}
		if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot["user_agent"] != c.UserAgent {
			t.Error("replacement snapshot is incomplete")
		}
		return previousReplace(from, to)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("save bypassed snapshot replacement")
	}
	if c.configFile != nil {
		t.Fatal("save retained a configuration handle")
	}
	other := NewConfig(c.FilePath())
	defer other.Close()
	if err := other.Init(); err != nil || other.UserAgent != c.UserAgent {
		t.Fatalf("saved config did not reopen: %v", err)
	}
	// Another instance can replace the pathname while this instance is alive.
	replaceConfigFile = previousReplace
	other.UserAgent = "updated by another synthetic instance"
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(); err != nil || c.UserAgent != other.UserAgent {
		t.Fatalf("reload used an old snapshot: %v", err)
	}
	if c.configFile != nil {
		t.Fatal("reload retained a configuration handle")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(c.FilePath())
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("config permissions changed: %v %v", info, err)
		}
	}
}

func TestConfigCloseWaitsForSave(t *testing.T) {
	c := reloadFixture(t)
	if err := c.Init(); err != nil {
		t.Fatal(err)
	}
	c.UserAgent = "synthetic saved value"
	previousWrite := writeConfigData
	defer func() { writeConfigData = previousWrite }()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	writeConfigData = func(w io.Writer, data []byte) (int, error) {
		close(entered)
		<-release
		return previousWrite(w, data)
	}
	saved := make(chan error, 1)
	go func() { saved <- c.Save() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not reach its write")
	}
	closed, started := make(chan error, 1), make(chan struct{})
	go func() { close(started); closed <- c.Close() }()
	<-started
	premature := false
	select {
	case err := <-closed:
		premature = true
		if err != nil {
			t.Error(err)
		}
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-saved:
		if err != nil {
			t.Errorf("close interrupted save: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save did not finish")
	}
	if !premature {
		select {
		case err := <-closed:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("close did not finish")
		}
	}
	if premature {
		t.Error("close returned before the pending save finished")
	}
	data, err := os.ReadFile(c.FilePath())
	var snapshot map[string]interface{}
	if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot["user_agent"] != c.UserAgent {
		t.Fatal("close left an incomplete config")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("repeated close failed: %v", err)
	}
}

func TestConfigSavePreservesSymlinkTarget(t *testing.T) {
	testConfigSymlink(t, true)
}

func TestConfigSaveInitializesSymlinkTarget(t *testing.T) {
	testConfigSymlink(t, false)
}

func testConfigSymlink(t *testing.T, existing bool) {
	t.Helper()
	root := t.TempDir()
	target, link := filepath.Join(root, "target.json"), filepath.Join(root, "config.json")
	if existing {
		if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	c := NewConfig(link)
	c.InitDefaultConfig()
	c.UserAgent = "synthetic linked config"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced the symbolic link")
	}
	data, err := os.ReadFile(target)
	var snapshot map[string]interface{}
	if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot["user_agent"] != c.UserAgent {
		t.Fatal("linked target was not saved")
	}
}

func TestConfigSaveRefusesReadOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"user_agent":"synthetic read-only config"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0600)
	if probe, err := os.OpenFile(path, os.O_RDWR, 0600); err == nil {
		probe.Close()
		t.Skip("current user can write read-only files")
	}
	c := NewConfig(path)
	c.InitDefaultConfig()
	if err := c.Save(); !errors.Is(err, ErrConfigFileNoPermission) {
		t.Fatalf("read-only file accepted: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("read-only config changed")
	}
}
