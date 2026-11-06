package baidupcs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestShareListEmptyObjectPreservesRemoteErrors(t *testing.T) {
	for _, payload := range []string{`{"errno":110,"list":{}}`, `{"list":{},"errno":110}`} {
		t.Run(payload, func(t *testing.T) {
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) { return response(r, payload), nil })
			records, err := pcs.ShareList(1)
			if err == nil || err.GetRemoteErrCode() != 110 || err.GetErrType() != pcserror.ErrTypeRemoteError || records != nil {
				t.Fatalf("remote error lost: records=%v err=%v", records, err)
			}
		})
	}
}

func invokeShare(pcs *BaiduPCS, ctx context.Context, action string, legacy bool) (int, error) {
	switch action {
	case "create":
		var shared *Shared
		var err error
		option := &ShareOption{Password: "Ab12", Period: 7}
		if legacy {
			shared, err = pcs.ShareSet([]string{"/one", "/two"}, option)
		} else {
			shared, err = pcs.ShareSetWithContext(ctx, []string{"/one", "/two"}, option)
		}
		if err != nil {
			return 0, err
		}
		if shared.Pwd != "Ab12" {
			return 0, errors.New("share password changed")
		}
		return int(shared.ShareID), nil
	case "list":
		var records ShareRecordInfoList
		var err error
		if legacy {
			records, err = pcs.ShareList(2)
		} else {
			records, err = pcs.ShareListWithContext(ctx, 2)
		}
		return len(records), err
	case "cancel":
		if legacy {
			return 0, pcs.ShareCancel([]int64{7, 8})
		}
		return 0, pcs.ShareCancelWithContext(ctx, []int64{7, 8})
	default:
		var info *ShareSURLInfo
		var err error
		if legacy {
			info, err = pcs.ShareSURLInfo(7)
		} else {
			info, err = pcs.ShareSURLInfoWithContext(ctx, 7)
		}
		if err != nil {
			return 0, err
		}
		if info.Pwd != "" {
			return 0, errors.New("zero password was not normalized")
		}
		return 1, nil
	}
}

const shareContextPayload = `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":7,"pwd":"0","shorturl":"fixture","list":[{"shareId":7}]}`

func TestShareContextCancellation(t *testing.T) {
	for _, action := range []string{"create", "list", "cancel", "details"} {
		for _, phase := range []string{"before", "headers", "body", "reply"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				calls := 0
				var body *shareTrackedBody
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
					}
					if phase == "headers" {
						close(started)
						select {
						case <-r.Context().Done():
							return nil, r.Context().Err()
						case <-release:
							return nil, errors.New("fixture forced release")
						}
					}
					body = &shareTrackedBody{Reader: locateBlockingReader{r.Context(), started, release}}
					if phase == "reply" {
						body.Reader = searchCancelReader{strings.NewReader(shareContextPayload), cancel}
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				if phase == "before" {
					cancel()
				}
				done, joined := make(chan error, 1), make(chan struct{})
				go func() { defer close(joined); _, err := invokeShare(pcs, ctx, action, false); done <- err }()
				defer func() { cancel(); unblock(); <-joined }()
				if phase == "headers" || phase == "body" {
					select {
					case <-started:
					case <-time.After(time.Second):
						t.Fatal("share request did not start")
					}
					cancel()
				}
				select {
				case err := <-done:
					want := 1
					if phase == "before" {
						want = 0
					}
					if !errors.Is(err, context.Canceled) || calls != want || (body != nil && !body.closed) {
						t.Fatalf("share cancellation lost: err=%v calls=%d body=%+v", err, calls, body)
					}
				case <-time.After(time.Second):
					t.Error("share request ignored cancellation")
				}
			})
		}
	}
}

func TestShareContextCompatibility(t *testing.T) {
	for _, action := range []string{"create", "list", "cancel", "details"} {
		for _, mode := range []string{"legacy", "context", "remote-error", "invalid-json"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				payload := shareContextPayload
				if mode == "remote-error" {
					payload = `{"errno":110}`
				}
				if mode == "invalid-json" {
					payload = `{`
				}
				body := &shareTrackedBody{Reader: strings.NewReader(payload)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					if r.Body != nil {
						defer r.Body.Close()
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					switch action {
					case "create":
						if r.Method != http.MethodPost || r.URL.Path != "/share/pset" || r.Form.Get("path_list") != `["/one","/two"]` || r.Form.Get("pwd") != "Ab12" || r.Form.Get("period") != "7" {
							t.Errorf("creation parameters changed: %s %s form=%v", r.Method, r.URL, r.Form)
						}
					case "list":
						if r.Method != http.MethodGet || r.URL.Path != "/share/record" || r.Form.Get("page") != "2" || r.Form.Get("order") != "time" || r.Form.Get("desc") != "1" {
							t.Errorf("listing parameters changed: %s %s", r.Method, r.URL)
						}
					case "cancel":
						if r.Method != http.MethodPost || r.URL.Path != "/share/cancel" || r.Form.Get("shareid_list") != "[7,8]" {
							t.Errorf("cancel parameters changed: %s %s form=%v", r.Method, r.URL, r.Form)
						}
					case "details":
						if r.Method != http.MethodGet || r.URL.Path != "/share/surlinfoinrecord" || r.Form.Get("shareid") != "7" || r.Form.Get("sign") == "" {
							t.Errorf("detail parameters changed: %s %s", r.Method, r.URL)
						}
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				count, err := invokeShare(pcs, context.Background(), action, mode == "legacy")
				if !body.closed {
					t.Fatal("share response not closed")
				}
				if mode == "remote-error" || mode == "invalid-json" {
					if err == nil || count != 0 {
						t.Fatalf("invalid response accepted: count=%d err=%v", count, err)
					}
					return
				}
				want := map[string]int{"create": 7, "list": 1, "cancel": 0, "details": 1}[action]
				if err != nil || count != want {
					t.Fatalf("share result changed: count=%d want=%d err=%v", count, want, err)
				}
			})
		}
	}
}

func TestShareListAcceptsEmptyObjectFormatting(t *testing.T) {
	for _, payload := range []string{`{"errno":0,"list":{}}`, `{"errno":0,"list":{ }}`} {
		t.Run(payload, func(t *testing.T) {
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) { return response(r, payload), nil })
			records, err := pcs.ShareList(1)
			if err != nil || len(records) != 0 {
				t.Fatalf("valid empty list rejected: records=%v err=%v", records, err)
			}
		})
	}
}

func TestShareDetailsEmptyObjectDoesNotPanic(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("empty details caused panic: %v", p)
		}
	}()
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) { return response(r, `{"errno":0}`), nil })
	info, err := pcs.ShareSURLInfo(7)
	if err == nil || info != nil {
		t.Fatalf("missing share details accepted: info=%v err=%v", info, err)
	}
}
