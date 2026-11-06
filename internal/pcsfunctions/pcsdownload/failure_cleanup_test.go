package pcsdownload

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func TestFailedDownloadCleanupRespectsFileOwnership(t *testing.T) {
	for _, mode := range []string{"existing-panic", "existing-error", "changed-path-panic", "new-error", "new-owner-panic", "new-nonempty-error", "new-nonempty-panic"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "output")
			other := filepath.Join(root, "valuable.empty")
			if err := os.WriteFile(other, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "existing-") {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			unit := emptyTask(path, true, false)
			unit.FileInfo.Size = 8
			failure := errors.New("synthetic transport failure")
			client := requester.NewHTTPClient()
			client.Client.Transport = downloadRoundTrip(func(r *http.Request) (*http.Response, error) {
				if mode == "new-error" || mode == "new-owner-panic" || mode == "existing-error" {
					return nil, failure
				}
				body := "abcdefgh"
				if strings.Contains(mode, "nonempty") {
					body = "abcd"
				}
				return &http.Response{StatusCode: 200, ContentLength: 8, Header: http.Header{"X-Bs-File-Size": []string{"8"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			var owner *downloader.InstanceState
			defer func() {
				if owner != nil {
					owner.Close()
				}
			}()
			switch mode {
			case "existing-panic":
				unit.OnProbe = func(string, string) { panic("synthetic probe panic") }
			case "changed-path-panic":
				unit.OnProbe = func(string, string) { unit.SavePath = other; panic("synthetic probe panic") }
			case "new-nonempty-panic":
				unit.OnTransferError = func(error) { panic("synthetic callback panic after partial write") }
			case "new-owner-panic":
				unit.OnTransferError = func(error) {
					file, err := os.OpenFile(path+DownloadSuffix, os.O_CREATE|os.O_RDWR, 0600)
					if err != nil {
						panic(err)
					}
					owner = downloader.NewInstanceState(file, downloader.InstanceStateStorageFormatJSON)
					if _, err := owner.Load(); err != nil {
						panic(fmt.Sprintf("failed to acquire later ownership: %v", err))
					}
					panic("synthetic callback panic after another owner acquired checkpoint")
				}
			}
			err := unit.download("https://fixture.invalid/file", client)
			if err == nil {
				t.Fatal("failed transfer reported success")
			}
			if (mode == "new-error" || mode == "existing-error") && !errors.Is(err, failure) {
				t.Errorf("original failure lost: %v", err)
			}
			if info, err := os.Stat(other); err != nil || info.Size() != 0 {
				t.Errorf("cleanup removed unrelated file: %v", err)
			}
			_, err = os.Stat(path)
			retain := strings.HasPrefix(mode, "existing-") || mode == "new-owner-panic" || strings.Contains(mode, "nonempty")
			if retain && err != nil {
				t.Errorf("cleanup removed an unowned output: %v", err)
			}
			if !retain && !os.IsNotExist(err) {
				t.Errorf("owned empty output was not cleaned: %v", err)
			}
			if strings.Contains(mode, "nonempty") {
				if got, err := os.ReadFile(path); err != nil || string(got) != "abcd" {
					t.Errorf("cleanup changed partial data: %q %v", got, err)
				}
			}
		})
	}
}

func TestEmptyCleanupRetainsDifferentFileIdentity(t *testing.T) {
	root := t.TempDir()
	owned, _, err := openDownloadOutput(filepath.Join(root, "original"))
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	current := filepath.Join(root, "replacement")
	if err := os.WriteFile(current, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedEmptyDownload(owned, current, current+DownloadSuffix); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatal("cleanup removed replacement:", err)
	}
	if _, err := owned.Stat(); err != nil {
		t.Fatal("identity mismatch closed unrelated handle:", err)
	}
}

func TestDownloadOutputSymlinkPreservesLinkOnFailure(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			root := t.TempDir()
			target, link := filepath.Join(root, "target"), filepath.Join(root, "link")
			if exists {
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("file symlink unavailable: %v", err)
			}
			file, created, err := openDownloadOutput(link)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if created == exists {
				t.Fatal("incorrect target creation ownership")
			}
			if created {
				if err := removeOwnedEmptyDownload(file, file.Name(), link+DownloadSuffix); err != nil {
					t.Fatal(err)
				}
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("cleanup removed output symlink: %v", err)
			}
			_, err = os.Stat(target)
			if exists && err != nil {
				t.Fatal("cleanup removed existing target:", err)
			}
			if !exists && !os.IsNotExist(err) {
				t.Fatal("cleanup retained owned empty target:", err)
			}
		})
	}
}
