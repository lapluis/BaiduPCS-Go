package baidupcs

import (
	"net/http"
	"testing"
)

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
