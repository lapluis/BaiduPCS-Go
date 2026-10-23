package baidupcs

import (
	"net/http"
	"testing"
)

func TestMutationsRejectNullResponse(t *testing.T) {
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { return response(req, "null"), nil })
	for name, mutate := range map[string]func() error{
		"copy":   func() error { return pcs.Copy(&CpMvJSON{From: "/a", To: "/b"}) },
		"move":   func() error { return pcs.Move(&CpMvJSON{From: "/a", To: "/b"}) },
		"remove": func() error { return pcs.Remove("/a") },
		"mkdir":  func() error { return pcs.Mkdir("/a") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := mutate(); err == nil {
				t.Fatal("null response reported as successful mutation")
			}
		})
	}
}
