package pcsupload

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestRenewedUploadSessionReachesCheckpointPartsAndMerge(t *testing.T) {
	for _, returned := range []string{"new-session", "old-session", ""} {
		t.Run("returned="+returned, func(t *testing.T) {
			root := t.TempDir()
			source, database := filepath.Join(root, "file"), filepath.Join(root, "resume.json")
			if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
				t.Fatal(err)
			}
			local := checksum.NewLocalFileChecksum(source, 4)
			if err := local.OpenPath(); err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			db, err := NewUploadingDatabaseAt(database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			state := &uploader.InstanceState{Uploadid: "old-session", BlockList: []*uploader.BlockState{{ID: 0, Range: transfer.Range{Begin: 0, End: 8}}}}
			db.UpdateUploading(&local.LocalFileMeta, state)
			if err := db.Save(); err != nil {
				t.Fatal(err)
			}
			want := returned
			if want == "" {
				want = "old-session"
			}
			var partID, mergeID string
			pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
				data := `{"error_code":1}` // Discovery is optional.
				status := http.StatusOK
				switch {
				case r.URL.Path == "/api/list":
					data = `{"errno":0,"list":[]}`
				case r.URL.Path == "/api/user/getinfo":
					data = `{"errno":0,"records":[{"uk":7}]}`
				case r.URL.Query().Get("method") == "meta":
					data = `{"error_code":31066}`
				case r.URL.Path == "/api/precreate":
					data = fmt.Sprintf(`{"errno":0,"return_type":1,"uploadid":%q}`, returned)
				case strings.HasSuffix(r.URL.Path, "/superfile2"):
					partID = r.URL.Query().Get("uploadid")
					if partID != want {
						status, data = http.StatusForbidden, `{"error_code":31066}`
					} else {
						if _, err := io.Copy(io.Discard, r.Body); err != nil {
							return nil, err
						}
						data = fmt.Sprintf(`{"md5":"%x"}`, md5.Sum([]byte("abcdefgh")))
					}
				case r.URL.Path == "/api/create":
					if err := r.ParseForm(); err != nil {
						return nil, err
					}
					mergeID = r.Form.Get("uploadid")
					data = `{"errno":0}`
					if mergeID != want {
						data = `{"errno":1}`
					}
				}
				return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			task := &UploadTaskUnit{Context: ctx, PCS: pu.pcs, LocalFileChecksum: local, UploadingDatabase: db, UploadStatistic: &UploadStatistic{}, Parallel: 1, SavePath: "/fixture", panDir: "/", panFile: "fixture", Policy: baidupcs.OverWritePolicy, state: state, taskInfo: &taskframework.TaskInfo{}}
			next, result := task.rapidUpload()
			if !next || result.Err != nil {
				t.Fatalf("precreate did not continue: next=%v result=%+v", next, result)
			}
			reloaded, err := NewUploadingDatabaseAt(database)
			if err != nil {
				t.Fatal(err)
			}
			saved := reloaded.Search(&local.LocalFileMeta)
			reloaded.Close()
			if task.state.Uploadid != want || saved == nil || saved.Uploadid != want {
				t.Errorf("renewed session not persisted: current=%q saved=%+v want=%q", task.state.Uploadid, saved, want)
			}
			result = task.upload()
			if !result.Succeed || partID != want || mergeID != want {
				t.Fatalf("transfer used stale session: part=%q merge=%q want=%q result=%+v", partID, mergeID, want, result)
			}
		})
	}
}
