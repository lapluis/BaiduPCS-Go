package baidupcs

import (
	"net/http"
	"sync/atomic"
	"testing"
)

func TestInflightListingCannotRestoreInvalidatedCache(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/list" && calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return response(req, `{"errno":0,"list":[]}`), nil
	})
	done := make(chan error, 1)
	go func() { _, err := pcs.CacheFilesDirectoriesList("/", nil); done <- err }()
	<-started
	err := pcs.Mkdir("/new")
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pcs.CacheFilesDirectoriesList("/", nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("pre-mutation listing was cached after mutation: %d list requests, want 2", calls.Load())
	}
}

func TestCredentialChangeInvalidatesDirectoryCache(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(req, `{"errno":0,"list":[]}`), nil
	})
	pcs.SetBDUSS("SYNTHETIC_OLD")
	if _, err := pcs.CacheFilesDirectoriesList("/", DefaultOrderOptions); err != nil {
		t.Fatal(err)
	}
	clone := pcs.CopyPCS()
	if _, err := clone.CacheFilesDirectoriesList("/", DefaultOrderOptions); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("directory cache not reused")
	}
	clone.SetBDUSS("SYNTHETIC_NEW")
	if _, err := pcs.CacheFilesDirectoriesList("/", DefaultOrderOptions); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("credential change reused prior account directory cache")
	}
}

func TestCopyPCSSharesCachePointer(t *testing.T) {
	pcs := NewPCS(1, "")
	copy := pcs.CopyPCS()
	if pcs.cacheOpMap == nil || pcs.cacheOpMap != copy.cacheOpMap {
		t.Fatal("copied client must share cache without copying locks")
	}
}

func TestDirectoryMutationInvalidatesAllSortOrders(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(req, `{"errno":0,"list":[]}`), nil
	})
	orders := []*OrderOptions{DefaultOrderOptions, {By: OrderByTime, Order: OrderDesc}, {By: OrderBySize, Order: OrderAsc}}
	for _, order := range orders {
		if _, err := pcs.CacheFilesDirectoriesList("/dir", order); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pcs.CacheFilesDirectoriesList("/other", DefaultOrderOptions); err != nil {
		t.Fatal(err)
	}
	pcs.deleteCache([]string{"/dir"})
	for _, order := range orders {
		if _, err := pcs.CacheFilesDirectoriesList("/dir", order); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pcs.CacheFilesDirectoriesList("/other", DefaultOrderOptions); err != nil {
		t.Fatal(err)
	}
	if calls != 7 {
		t.Fatalf("changed directory sort caches were not invalidated independently: %d requests", calls)
	}
}

func TestDirectoryCacheAcceptsDefaultOptions(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatal("nil sort options panicked")
		}
	}()
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { return response(req, `{"errno":0,"list":[]}`), nil })
	if _, err := pcs.CacheFilesDirectoriesList("/", nil); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryCacheRootAliases(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(req, `{"errno":0,"list":[]}`), nil
	})
	for _, dir := range []string{"", "/", "//", "/./"} {
		if _, err := pcs.CacheFilesDirectoriesList(dir, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("root aliases did not share cache: %d requests", calls)
	}
	if err := pcs.Mkdir("/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := pcs.CacheFilesDirectoriesList("", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("root listing stayed cached after mkdir: %d requests, want 3", calls)
	}
}

func TestDirectoryMutationInvalidatesSubtrees(t *testing.T) {
	for _, operation := range []string{"remove", "move", "rename", "copy"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
				calls++
				return response(req, `{"errno":0,"list":[]}`), nil
			})
			paths := []string{"/", "/dir", "/dir/sub", "/dir-other", "/other", "/new", "/new/sub"}
			for _, dir := range paths {
				if _, err := pcs.CacheFilesDirectoriesList(dir, nil); err != nil {
					t.Fatal(err)
				}
			}
			switch operation {
			case "remove":
				if err := pcs.Remove("/dir/"); err != nil {
					t.Fatal(err)
				}
			case "move":
				if err := pcs.Move(&CpMvJSON{From: "/dir", To: "/new"}); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if err := pcs.Rename("/dir", "/new"); err != nil {
					t.Fatal(err)
				}
			case "copy":
				if err := pcs.Copy(&CpMvJSON{From: "/dir", To: "/new"}); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range paths {
				before := calls
				if _, err := pcs.CacheFilesDirectoriesList(dir, nil); err != nil {
					t.Fatal(err)
				}
				changed := dir == "/" || (operation != "copy" && (dir == "/dir" || dir == "/dir/sub")) || (operation != "remove" && (dir == "/new" || dir == "/new/sub"))
				if (calls != before) != changed {
					t.Errorf("%s: refreshed=%v, want %v", dir, calls != before, changed)
				}
			}
		})
	}
}
