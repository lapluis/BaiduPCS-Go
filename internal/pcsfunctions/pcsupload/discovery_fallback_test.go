package pcsupload

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUploadDiscoveryPreservesUsableServer(t *testing.T) {
	for _, tc := range []struct{ name, payload, selected string }{
		{"null-only", `{"error_code":0,"servers":[null]}`, "original-fixture.invalid"},
		{"mixed", `{"error_code":0,"servers":[null,{"server":"https://next-fixture.invalid/path"}]}`, "next-fixture.invalid"},
		{"legacy", `{"error_code":0,"servers":[null],"server":["legacy-fixture.invalid"]}`, "legacy-fixture.invalid"},
		{"error", `{"error_code":1,"servers":[null]}`, "original-fixture.invalid"},
	} {
		for _, phase := range []string{"prepare", "periodic"} {
			t.Run(tc.name+"/"+phase, func(t *testing.T) {
				calls := 0
				payload := []byte("data")
				bodyClosures := 0
				pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
					calls++
					data := tc.payload
					if calls > 1 {
						if _, err := io.Copy(io.Discard, r.Body); err != nil {
							return nil, err
						}
						data = fmt.Sprintf(`{"md5":"%x"}`, md5.Sum(payload))
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &discoveryResponseBody{Reader: strings.NewReader(data), closed: &bodyClosures}, Request: r}, nil
				})
				pu.pcs.SetPCSAddr("original-fixture.invalid")
				if phase == "prepare" {
					origin, err := pu.Precreate(context.Background())
					if err != nil || origin != "original-fixture.invalid" || calls != 1 {
						t.Fatal("prepare fallback failed", origin, err, calls)
					}
				} else {
					sum, err := pu.TmpFile(context.Background(), "session", "/fixture", pcsPeriod-1, 0, uploadTestReader{bytes.NewReader(payload)})
					if err != nil || sum != fmt.Sprintf("%x", md5.Sum(payload)) || calls != 2 {
						t.Fatal("periodic discovery interrupted part upload", sum, err, calls)
					}
				}
				if pu.pcs.GetPCSAddr() != tc.selected || bodyClosures != calls {
					t.Fatal("server selection or response cleanup incorrect", pu.pcs.GetPCSAddr(), tc.selected, bodyClosures, calls)
				}
			})
		}
	}
}

type discoveryResponseBody struct {
	*strings.Reader
	closed *int
}

func (b *discoveryResponseBody) Close() error { *b.closed++; return nil }

func TestUploadDiscoveryCancellationDoesNotContinue(t *testing.T) {
	for _, periodic := range []bool{false, true} {
		t.Run(fmt.Sprint(periodic), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				cancel()
				return nil, ctx.Err()
			})
			pu.pcs.SetPCSAddr("original-fixture.invalid")
			var err error
			if periodic {
				_, err = pu.TmpFile(ctx, "session", "/fixture", pcsPeriod-1, 0, uploadTestReader{bytes.NewReader(nil)})
			} else {
				_, err = pu.Precreate(ctx)
			}
			if !errors.Is(err, context.Canceled) || calls != 1 || pu.pcs.GetPCSAddr() != "original-fixture.invalid" {
				t.Fatal("canceled discovery continued or changed server", err, calls, pu.pcs.GetPCSAddr())
			}
		})
	}
}
