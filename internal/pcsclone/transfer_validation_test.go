package pcsclone

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestTransferRejectsInvalidShareBeforeRemoteOperations(t *testing.T) {
	for _, tc := range []struct {
		name, link, code string
	}{
		{"malformed", "not-a-link", "abcd"},
		{"foreign-host", "https://fixture.invalid/s/1fixture", "abcd"},
		{"bad-feature", "https://pan.baidu.com/s/x", "abcd"},
		{"missing-code", "https://pan.baidu.com/s/1fixture", ""},
		{"bad-inline-code", "https://pan.baidu.com/s/1fixture?pwd=abcde", ""},
		{"bad-code-characters", "https://pan.baidu.com/s/1fixture", "@@@@"},
		{"upper-scheme-foreign-embedded", "HTTPS://fixture.invalid/?next=https://pan.baidu.com/s/1AbC_fixture?pwd=AbCd", ""},
		{"upper-scheme-userinfo-embedded", "HTTPS://pan.baidu.com@fixture.invalid/?next=https://pan.baidu.com/s/1AbC_fixture?pwd=AbCd", ""},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonOutput), func(t *testing.T) {
				var requests, mkdir atomic.Int32
				flags := []string{}
				if tc.code != "" {
					flags = append(flags, "--code", tc.code)
				}
				if jsonOutput {
					flags = append(flags, "--json")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, req *http.Request) {
					requests.Add(1)
					if req.URL.Query().Get("method") == "mkdir" {
						mkdir.Add(1)
						_, _ = io.WriteString(w, `{"error_code":0}`)
					} else {
						_, _ = io.WriteString(w, `{"error_code":31066}`)
					}
				}, "transfer", []string{tc.link, "bd:/new-directory"}, flags)
				err := c.cmd.Run(c)
				var usage *exitError
				if !errors.As(err, &usage) || usage.code != 2 || requests.Load() != 0 || mkdir.Load() != 0 || out.Len() != 0 {
					t.Fatalf("invalid share reached remote operations: err=%v requests=%d mkdir=%d output=%q", err, requests.Load(), mkdir.Load(), out.String())
				}
			})
		}
	}
}
