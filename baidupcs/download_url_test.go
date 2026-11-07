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
	for _, raw := range []string{"", "/relative", "cdn.fixture.invalid/file", "ftp://cdn.fixture.invalid/file", "file:///tmp/file", "mailto:fixture@example.invalid", "https:///missing-host", "http://:80/file", "http://synthetic:secret@cdn.fixture.invalid/file", "http://cdn.fixture.invalid/%zz", "https://cdn.fixture.invalid:65536/file", "https://cdn.fixture.invalid:0/file", "http://2001:db8::1/file", "http://2001:db8::1:8080/file"} {
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

func TestDownloadURLSelectorsPreserveValidAuthorities(t *testing.T) {
	for _, authority := range []string{"cdn.fixture.invalid:1", "cdn.fixture.invalid:65535", "[2001:db8::1]", "[2001:db8::1]:8443", "[fe80::1%25fixture]:8080"} {
		t.Run(authority, func(t *testing.T) {
			for _, prefix := range []string{"http:", "https:", ""} {
				info := downloadURLFixture(t, map[string]interface{}{"url": prefix + "//" + authority + "/a%2Fb?sign=synthetic%2Bvalue&x=1&x=2", "encrypt": 0})
				for _, https := range []bool{false, true} {
					want := GetHTTPScheme(https) + "://" + authority + "/a%2Fb?sign=synthetic%2Bvalue&x=1&x=2"
					all, first, last := info.URLStrings(https), info.SingleURL(https), info.LastURL(https)
					if len(all) != 1 || first == nil || last == nil || all[0].String() != want || first.String() != want || last.String() != want {
						t.Fatalf("valid authority or signed URL changed: %v %v %v", all, first, last)
					}
				}
			}
		})
	}
}

func TestDownloadURLSelectorsPreserveEligibleOrderAndSignedQuery(t *testing.T) {
	info := downloadURLFixture(t,
		map[string]interface{}{"url": "", "encrypt": 0},
		map[string]interface{}{"url": "https://invalid.fixture.invalid:65536/file", "encrypt": 0},
		map[string]interface{}{"url": "http://first.fixture.invalid:8443/a%2Fb?token=synthetic%2Bvalue&x=1&x=2", "encrypt": 0},
		map[string]interface{}{"url": "https://encrypted.fixture.invalid/file", "encrypt": 1},
		map[string]interface{}{"url": "//last.fixture.invalid/file?sign=synthetic", "encrypt": 0},
		map[string]interface{}{"url": "/relative", "encrypt": 0},
		map[string]interface{}{"url": "https://encrypted-last.fixture.invalid/file", "encrypt": 1},
		map[string]interface{}{"url": "http://2001:db8::1/file", "encrypt": 0},
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
