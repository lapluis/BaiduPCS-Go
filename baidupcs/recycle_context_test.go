package baidupcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func invokeRecycle(pcs *BaiduPCS, ctx context.Context, action string, legacy bool) (int, error) {
	switch action {
	case "list":
		var files RecycleFDInfoList
		var err error
		if legacy {
			files, err = pcs.RecycleList(2)
		} else {
			files, err = pcs.RecycleListWithContext(ctx, 2)
		}
		return len(files), err
	case "restore":
		var files []*FsIDJSON
		var err error
		if legacy {
			files, err = pcs.RecycleRestore(7, 8)
		} else {
			files, err = pcs.RecycleRestoreWithContext(ctx, 7, 8)
		}
		return len(files), err
	case "delete":
		if legacy {
			return 0, pcs.RecycleDelete(7, 8)
		}
		return 0, pcs.RecycleDeleteWithContext(ctx, 7, 8)
	default:
		if legacy {
			return pcs.RecycleClear()
		}
		return pcs.RecycleClearWithContext(ctx)
	}
}

func TestRecycleResponsesAndContext(t *testing.T) {
	for _, action := range []string{"list", "restore", "delete", "clear"} {
		for _, mode := range []string{"legacy-success", "context-success", "before", "canceled-reply", "remote-error", "invalid-json"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "before" {
					cancel()
				}
				calls := 0
				var body *locateBody
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
					}
					switch action {
					case "list":
						if r.Method != http.MethodGet || r.URL.Path != "/api/recycle/list" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("num") != "100" {
							t.Errorf("listing request changed: %s %s", r.Method, r.URL)
						}
					case "restore":
						if r.Method != http.MethodPost || r.URL.Query().Get("method") != "restore" {
							t.Errorf("restore endpoint changed: %s %s", r.Method, r.URL)
						}
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Fatal(err)
						}
						defer r.MultipartForm.RemoveAll()
						var ids FsIDListJSON
						if err := json.Unmarshal([]byte(r.FormValue("param")), &ids); err != nil || len(ids.List) != 2 {
							t.Fatalf("restore form invalid: %q (%v)", r.FormValue("param"), err)
						}
						if ids.List[0].FsID != 7 || ids.List[1].FsID != 8 {
							t.Errorf("restore IDs changed: %+v", ids.List)
						}
					case "delete":
						if err := r.ParseForm(); err != nil {
							t.Fatal(err)
						}
						if r.Method != http.MethodPost || r.URL.Path != "/api/recycle/delete" || r.Form.Get("fidlist") != "[7,8]" {
							t.Errorf("deletion request changed: %s %s form=%v", r.Method, r.URL, r.Form)
						}
					case "clear":
						if r.Method != http.MethodGet || r.URL.Query().Get("method") != "delete" || r.URL.Query().Get("type") != "recycle" {
							t.Errorf("clear request changed: %s %s", r.Method, r.URL)
						}
					}
					payload := `{"errno":0,"error_code":0,"list":[{"fs_id":7,"path":"/fixture","size":9}],"extra":{"list":[{"fs_id":7},{"fs_id":8}],"succNum":3}}`
					if mode == "remote-error" {
						payload = `{"errno":31066,"error_code":31066}`
					}
					if mode == "invalid-json" {
						payload = `{`
					}
					body = &locateBody{Reader: strings.NewReader(payload)}
					if mode == "canceled-reply" {
						body.Reader = searchCancelReader{body.Reader, cancel}
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				count, err := invokeRecycle(pcs, ctx, action, mode == "legacy-success")
				if mode == "before" {
					if calls != 0 || !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled request sent: calls=%d err=%v", calls, err)
					}
					return
				}
				if calls != 1 || body == nil || !body.closed {
					t.Fatalf("response not closed: calls=%d body=%+v", calls, body)
				}
				switch mode {
				case "legacy-success", "context-success":
					want := map[string]int{"list": 1, "restore": 2, "delete": 0, "clear": 3}[action]
					if err != nil || count != want {
						t.Fatalf("result changed: count=%d want=%d err=%v", count, want, err)
					}
				case "canceled-reply":
					if !errors.Is(err, context.Canceled) || count != 0 {
						t.Fatalf("canceled response accepted: count=%d err=%v", count, err)
					}
				default:
					if err == nil || count != 0 {
						t.Fatalf("invalid reply accepted: count=%d err=%v", count, err)
					}
				}
			})
		}
	}
}
