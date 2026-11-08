package baidupcs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type shareOptionReader struct {
	io.Reader
	once   sync.Once
	change func()
}

func (r *shareOptionReader) Read(p []byte) (int, error) {
	r.once.Do(r.change)
	return r.Reader.Read(p)
}

func TestShareSetKeepsSubmittedPassword(t *testing.T) {
	for _, phase := range []string{"response", "decode"} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", phase, contextual), func(t *testing.T) {
				option := &ShareOption{Password: "Ab12", Period: 7}
				change := func() { option.Password, option.Period, option.IsCombined = "Cd34", 1, true }
				body := &shareTrackedBody{Reader: strings.NewReader(`{"errno":0,"link":"https://fixture.invalid/s/one","shareid":7}`)}
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					defer req.Body.Close()
					if err := req.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if req.FormValue("pwd") != "Ab12" || req.FormValue("period") != "7" || req.FormValue("path_list") != `["/one"]` {
						t.Fatal("share request options changed")
					}
					if phase == "response" {
						change()
					} else {
						body.Reader = &shareOptionReader{Reader: body.Reader, change: change}
					}
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				var shared *Shared
				var err pcserror.Error
				if contextual {
					shared, err = pcs.ShareSetWithContext(context.Background(), []string{"/one"}, option)
				} else {
					shared, err = pcs.ShareSet([]string{"/one"}, option)
				}
				if err != nil || shared == nil || shared.Pwd != "Ab12" || shared.ShareID != 7 || !body.closed {
					t.Fatalf("submitted password lost: result=%+v err=%v closed=%t", shared, err, body.closed)
				}
				if option.Password != "Cd34" || option.Period != 1 || !option.IsCombined {
					t.Fatal("caller options were overwritten")
				}
			})
		}
	}
}

func TestShareSetRejectsNilOptionsWithoutRequest(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(fmt.Sprint(contextual), func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("nil options panicked: %v", recovered)
				}
			}()
			calls := 0
			pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
				calls++
				return response(req, `{"errno":0}`), nil
			})
			var shared *Shared
			var err pcserror.Error
			if contextual {
				shared, err = pcs.ShareSetWithContext(context.Background(), []string{"/one"}, nil)
			} else {
				shared, err = pcs.ShareSet([]string{"/one"}, nil)
			}
			if shared != nil || err == nil || err.GetErrType() != pcserror.ErrTypeOthers || calls != 0 {
				t.Fatalf("nil options were not rejected locally: shared=%v err=%v calls=%d", shared, err, calls)
			}
		})
	}
}

func TestShareSetGeneratedPasswordKeepsCallerOptions(t *testing.T) {
	for _, password := range []string{"", "x", "too-long"} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/context=%t", password, contextual), func(t *testing.T) {
				option := &ShareOption{Password: password, Period: 7}
				var submitted string
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					defer req.Body.Close()
					if err := req.ParseForm(); err != nil {
						t.Fatal(err)
					}
					submitted = req.FormValue("pwd")
					if len(submitted) != 4 || req.FormValue("period") != "7" {
						t.Fatal("generated password or period invalid")
					}
					return response(req, `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":7}`), nil
				})
				var shared *Shared
				var err pcserror.Error
				if contextual {
					shared, err = pcs.ShareSetWithContext(context.Background(), []string{"/one"}, option)
				} else {
					shared, err = pcs.ShareSet([]string{"/one"}, option)
				}
				if err != nil || shared == nil || shared.Pwd != submitted || option.Password != password || option.Period != 7 {
					t.Fatalf("generated password mismatch or caller mutation: shared=%+v err=%v option=%+v", shared, err, option)
				}
			})
		}
	}
}
