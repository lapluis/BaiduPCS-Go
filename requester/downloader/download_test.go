package downloader

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRandomNumber(t *testing.T) {
	for i := 0; i < 100; i++ {
		v := RandomNumber(0, 5)
		if v < 0 || v > 5 {
			t.Fatalf("out of range: %d", v)
		}
	}
}

// An owned HTTP server replaces the obsolete external QQ/image downloads.
func TestDownloadRangeContent(t *testing.T) {
	content := bytes.Repeat([]byte("range-transfer-fixture\n"), 100000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture.bin", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	dir := t.TempDir()
	filename := filepath.Join(dir, "download.bin")
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 3, CacheSize: 8192, InstanceStatePath: filepath.Join(dir, "state")})
	if err = d.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %d bytes want %d", len(got), len(content))
	}
}
func newSlice() [][]byte {
	s := make([][]byte, 20)
	s[0] = []byte("kjashdfiuqwheirhwuq")
	s[9] = []byte("kjashdfiuqwheirhwuq")
	return s
}

func rangeSlice(f func(key int, by []byte) bool) {
	s := newSlice()
	for k := range s {
		if s[k] == nil {
			continue
		}
		if !f(k, s[k]) {
			break
		}
	}
}

func BenchmarkRange1(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var a = 0
		rangeSlice(func(key int, s []byte) bool {
			a++
			return true
		})
	}
}

func BenchmarkRange2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := newSlice()
		a := 0
		for k := range s {
			if s[k] == nil {
				continue
			}
			a++
		}
	}
}
