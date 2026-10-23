package pcsdownload

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestDownloadProbeDoesNotMistakeMissingSizeForEmptyFile(t *testing.T) {
	for _, tc := range []struct {
		name, size, contentRange, body string
		status                         int
		want                           int64
		invalid                        bool
	}{
		{"full_without_custom_header", "", "", "data", 200, 4, false},
		{"partial_without_custom_header", "", "bytes 0-3/12", "data", 206, 12, false},
		{"empty", "", "", "", 200, 0, false},
		{"custom_size", "12", "bytes 0-3/12", "data", 206, 12, false},
		{"malformed_custom_size", "bad", "", "data", 200, 0, true},
		{"negative_custom_size", "-1", "", "data", 200, 0, true},
		{"false_empty", "0", "", "data", 200, 0, true},
		{"partial_missing_range", "", "", "data", 206, 0, true},
		{"partial_wrong_unit", "", "items 0-3/12", "data", 206, 0, true},
		{"partial_invalid_bounds", "", "bytes 5-3/12", "data", 206, 0, true},
		{"conflicting_sizes", "16", "bytes 0-3/12", "data", 206, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.size != "" {
					w.Header().Set("x-bs-file-size", tc.size)
				}
				if tc.contentRange != "" {
					w.Header().Set("Content-Range", tc.contentRange)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			size, resp, err := BaiduPCSURLCheckFunc(requester.NewHTTPClient(), server.URL)
			if resp != nil {
				defer resp.Body.Close()
			}
			if tc.invalid {
				if err == nil {
					t.Fatalf("invalid size metadata accepted as %d", size)
				}
			} else if err != nil || size != tc.want {
				t.Fatalf("size=%d, want %d, error=%v", size, tc.want, err)
			}
		})
	}
}
