package baidupcs

import (
	"encoding/json"
	"testing"
)

func downloadURLFixture(t *testing.T, entries ...map[string]interface{}) *URLInfo {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{"urls": entries})
	if err != nil {
		t.Fatal(err)
	}
	var info URLInfo
	if err := json.Unmarshal(payload, &info); err != nil {
		t.Fatal(err)
	}
	return &info
}

func TestDownloadURLSelectorsRejectUnusableLinks(t *testing.T) {
	for _, raw := range []string{"", "/relative", "cdn.fixture.invalid/file", "ftp://cdn.fixture.invalid/file", "file:///tmp/file", "mailto:fixture@example.invalid", "https:///missing-host", "http://:80/file", "http://synthetic:secret@cdn.fixture.invalid/file", "http://cdn.fixture.invalid/%zz"} {
		t.Run(raw, func(t *testing.T) {
			info := downloadURLFixture(t, map[string]interface{}{"url": raw, "encrypt": 0})
			for _, https := range []bool{false, true} {
				if all, first, last := info.URLStrings(https), info.SingleURL(https), info.LastURL(https); len(all) != 0 || first != nil || last != nil {
					t.Errorf("unusable URL selected: https=%v all=%v first=%v last=%v", https, all, first, last)
				}
			}
		})
	}
	info := downloadURLFixture(t, map[string]interface{}{"url": "https://cdn.fixture.invalid/encrypted", "encrypt": 1})
	if len(info.URLStrings(true)) != 0 || info.SingleURL(true) != nil || info.LastURL(true) != nil {
		t.Fatal("encrypted-only list produced a usable link")
	}
}

func TestDownloadURLSelectorsPreserveEligibleOrderAndSignedQuery(t *testing.T) {
	info := downloadURLFixture(t,
		map[string]interface{}{"url": "", "encrypt": 0},
		map[string]interface{}{"url": "http://first.fixture.invalid:8443/a%2Fb?token=synthetic%2Bvalue&x=1&x=2", "encrypt": 0},
		map[string]interface{}{"url": "https://encrypted.fixture.invalid/file", "encrypt": 1},
		map[string]interface{}{"url": "//last.fixture.invalid/file?sign=synthetic", "encrypt": 0},
		map[string]interface{}{"url": "/relative", "encrypt": 0},
		map[string]interface{}{"url": "https://encrypted-last.fixture.invalid/file", "encrypt": 1},
	)
	for _, https := range []bool{false, true} {
		scheme := GetHTTPScheme(https)
		wantFirst := scheme + "://first.fixture.invalid:8443/a%2Fb?token=synthetic%2Bvalue&x=1&x=2"
		wantLast := scheme + "://last.fixture.invalid/file?sign=synthetic"
		all, first, last := info.URLStrings(https), info.SingleURL(https), info.LastURL(https)
		if len(all) != 2 || first == nil || last == nil {
			t.Fatalf("eligible links lost: all=%v first=%v last=%v", all, first, last)
		}
		if all[0].String() != wantFirst || all[1].String() != wantLast || first.String() != wantFirst || last.String() != wantLast {
			t.Fatalf("order or signed query changed: all=%v first=%v last=%v", all, first, last)
		}
	}
	empty := &URLInfo{}
	if len(empty.URLStrings(true)) != 0 || empty.SingleURL(true) != nil || empty.LastURL(true) != nil {
		t.Fatal("empty list produced a link")
	}
}
