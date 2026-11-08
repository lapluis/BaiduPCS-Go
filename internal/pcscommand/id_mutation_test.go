package pcscommand

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestIDMutationValidationAndErrors(t *testing.T) {
	for _, op := range []string{"share", "delete", "restore", "clear"} {
		states := []string{"success", "remote", "network", "malformed"}
		if op != "clear" {
			states = append(states, "bad", "empty")
		}
		if op == "restore" {
			states = append(states, "partial")
		}
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				requests := 0
				cause := errors.New("fixture ID operation transport failure")
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Body != nil {
						defer req.Body.Close()
					}
					var ids []int64
					switch op {
					case "clear":
						if req.Method != http.MethodGet || req.URL.Query().Get("method") != "delete" || req.URL.Query().Get("type") != "recycle" {
							t.Fatalf("wrong clear request: %s %s", req.Method, req.URL)
						}
					case "restore":
						if req.URL.Query().Get("method") != "restore" {
							t.Fatalf("wrong restore request: %s", req.URL)
						}
						if err := req.ParseMultipartForm(4096); err != nil {
							t.Fatal(err)
						}
						if req.MultipartForm != nil {
							defer req.MultipartForm.RemoveAll()
						}
						var params baidupcs.FsIDListJSON
						if err := json.Unmarshal([]byte(req.FormValue("param")), &params); err != nil {
							t.Fatal(err)
						}
						for _, entry := range params.List {
							if entry == nil {
								t.Fatal("nil restore ID")
							}
							ids = append(ids, entry.FsID)
						}
					default:
						field, wantPath := "shareid_list", "/share/cancel"
						if op == "delete" {
							field, wantPath = "fidlist", "/api/recycle/delete"
						}
						if req.URL.Path != wantPath {
							t.Fatalf("unexpected request: %s", req.URL)
						}
						if err := req.ParseForm(); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal([]byte(req.FormValue(field)), &ids); err != nil {
							t.Fatal(err)
						}
					}
					if op != "clear" && !reflect.DeepEqual(ids, []int64{101, 202, 101}) {
						t.Fatalf("valid IDs changed: %v", ids)
					}
					if state == "network" {
						return nil, cause
					}
					payload := `{"errno":0,"error_code":0,"extra":{"list":[{"fs_id":101},{"fs_id":202}],"succNum":2}}`
					if state == "remote" {
						payload = `{"error_code":31045}`
					}
					if state == "partial" {
						payload = `{"error_code":31045,"extra":{"list":[{"fs_id":101}]}}`
					}
					if state == "malformed" {
						payload = `{`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				ids := []int64{101, 202, 101}
				values := []string{"101", "+202", "00101"}
				if state == "bad" {
					ids = []int64{101, -1}
					values = []string{"101", "bad"}
				}
				if state == "empty" {
					ids = nil
					values = nil
				}
				var got error
				log := captureExportCall(t, func() {
					switch op {
					case "share":
						got = RunShareCancelWithError(ids)
					case "delete":
						got = RunRecycleDeleteWithError(values...)
					case "restore":
						got = RunRecycleRestoreWithError(values...)
					case "clear":
						got = RunRecycleClearWithError()
					}
				})
				if (got == nil) != (state == "success") {
					t.Fatalf("unexpected operation result: %v", got)
				}
				wantRequests := 1
				if state == "bad" || state == "empty" {
					wantRequests = 0
				}
				if requests != wantRequests {
					t.Errorf("requests=%d want %d", requests, wantRequests)
				}
				if strings.Contains(log, "成功") != (state == "success" || state == "partial") {
					t.Errorf("wrong success diagnostic: %q", log)
				}
				if state == "network" && !errors.Is(got, cause) {
					t.Errorf("transport cause lost: %v", got)
				}
				if state == "remote" || state == "partial" {
					var remote pcserror.Error
					if !errors.As(got, &remote) || remote.GetRemoteErrCode() != 31045 {
						t.Errorf("remote cause lost: %v", got)
					}
				}
				if state == "partial" && (!strings.Contains(log, "以下的 fs_id 还原成功") || !strings.Contains(log, "101\n")) {
					t.Errorf("partial restore output lost: %q", log)
				}
			})
		}
	}
}
