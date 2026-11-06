package baidupcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func invokeMutation(ctx context.Context, pcs *BaiduPCS, action string, legacy bool) error {
	entry := &CpMvJSON{From: "/dir", To: "/new"}
	switch action {
	case "remove":
		if legacy {
			return pcs.Remove("/dir")
		}
		return pcs.RemoveWithContext(ctx, "/dir")
	case "copy":
		if legacy {
			return pcs.Copy(entry)
		}
		return pcs.CopyWithContext(ctx, entry)
	case "move":
		if legacy {
			return pcs.Move(entry)
		}
		return pcs.MoveWithContext(ctx, entry)
	case "rename":
		if legacy {
			return pcs.Rename("/dir", "/new")
		}
		return pcs.RenameWithContext(ctx, "/dir", "/new")
	default:
		if legacy {
			return pcs.Mkdir("/new")
		}
		return pcs.MkdirWithContext(ctx, "/new")
	}
}

func TestUncertainMutationInvalidatesAffectedCache(t *testing.T) {
	for _, action := range []string{"remove", "copy", "move", "rename", "mkdir"} {
		for _, phase := range []string{"headers", "reply", "network"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				listCalls := 0
				transportErr := errors.New("fixture transport failure")
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/api/list" {
						listCalls++
						return response(r, `{"errno":0,"list":[]}`), nil
					}
					if r.Body != nil {
						defer r.Body.Close()
					}
					if phase == "network" {
						return nil, transportErr
					}
					if phase == "headers" {
						cancel()
						return nil, ctx.Err()
					}
					body := &shareTrackedBody{Reader: searchCancelReader{strings.NewReader(`{"error_code":0}`), cancel}}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				for _, dir := range []string{"/", "/other"} {
					if _, err := pcs.CacheFilesDirectoriesList(dir, nil); err != nil {
						t.Fatal(err)
					}
				}
				wantErr := error(context.Canceled)
				if phase == "network" {
					wantErr = transportErr
				}
				if err := invokeMutation(ctx, pcs, action, false); !errors.Is(err, wantErr) {
					t.Fatalf("mutation cause lost: %v", err)
				}
				if _, err := pcs.CacheFilesDirectoriesList("/", nil); err != nil {
					t.Fatal(err)
				}
				if listCalls != 3 {
					t.Fatalf("uncertain mutation left root cache stale: list requests=%d want=3", listCalls)
				}
				if _, err := pcs.CacheFilesDirectoriesList("/other", nil); err != nil {
					t.Fatal(err)
				}
				if listCalls != 3 {
					t.Fatal("unrelated cache invalidated")
				}
			})
		}
	}
}

func TestMutationContextResponses(t *testing.T) {
	for _, action := range []string{"remove", "copy", "move", "rename", "mkdir"} {
		for _, mode := range []string{"legacy", "context", "before", "reply", "remote-error", "invalid-json"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "before" {
					cancel()
				}
				calls := 0
				var body *shareTrackedBody
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
					}
					method := action
					if action == "remove" {
						method = "delete"
					}
					if action == "rename" {
						method = "move"
					}
					if r.Method != http.MethodPost || r.URL.Query().Get("method") != method {
						t.Errorf("mutation endpoint changed: %s %s", r.Method, r.URL)
					}
					if action == "mkdir" {
						if r.URL.Query().Get("path") != "/new" {
							t.Error("mkdir path changed")
						}
					} else {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Fatal(err)
						}
						defer r.MultipartForm.RemoveAll()
						if action == "remove" {
							var param PathsListJSON
							if err := json.Unmarshal([]byte(r.FormValue("param")), &param); err != nil || len(param.List) != 1 {
								t.Fatalf("invalid delete form: %q (%v)", r.FormValue("param"), err)
							}
							if param.List[0].Path != "/dir" {
								t.Error("delete path changed")
							}
						} else {
							var param CpMvListJSON
							if err := json.Unmarshal([]byte(r.FormValue("param")), &param); err != nil || len(param.List) != 1 {
								t.Fatalf("invalid move/copy form: %q (%v)", r.FormValue("param"), err)
							}
							if param.List[0].From != "/dir" || param.List[0].To != "/new" {
								t.Error("move/copy paths changed")
							}
						}
					}
					payload := `{"error_code":0}`
					if mode == "remote-error" {
						payload = `{"error_code":31066}`
					}
					if mode == "invalid-json" {
						payload = `{`
					}
					body = &shareTrackedBody{Reader: strings.NewReader(payload)}
					if mode == "reply" {
						body.Reader = searchCancelReader{body.Reader, cancel}
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				err := invokeMutation(ctx, pcs, action, mode == "legacy")
				if mode == "before" {
					if !errors.Is(err, context.Canceled) || calls != 0 {
						t.Fatalf("canceled mutation sent: err=%v calls=%d", err, calls)
					}
					return
				}
				if calls != 1 || body == nil || !body.closed {
					t.Fatalf("response not closed: calls=%d body=%+v", calls, body)
				}
				switch mode {
				case "legacy", "context":
					if err != nil {
						t.Fatal(err)
					}
				case "reply":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled success accepted: %v", err)
					}
				case "remote-error":
					var remoteErr pcserror.Error
					if !errors.As(err, &remoteErr) || remoteErr.GetRemoteErrCode() != 31066 {
						t.Fatalf("remote error lost: %v", err)
					}
				case "invalid-json":
					if err == nil {
						t.Fatal("invalid JSON accepted")
					}
				}
			})
		}
	}
}
