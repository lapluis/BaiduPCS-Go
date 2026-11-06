package pcsupload

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestNoRapidUploadPersistsInitialSessionBeforePartsStart(t *testing.T) {
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
	requests := 0
	client := requester.NewHTTPClient()
	client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Body != nil {
			r.Body.Close()
		}
		var data string
		if r.URL.Query().Get("method") == "meta" {
			data = `{"error_code":31066,"error_msg":"not found"}`
		} else if r.URL.Path == "/api/precreate" {
			data = `{"errno":0,"return_type":1,"uploadid":"initial-fixture-session"}`
		} else {
			t.Errorf("unexpected request before part dispatch: %s", r.URL.Path)
			data = `{"errno":1}`
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
	})
	task := &UploadTaskUnit{LocalFileChecksum: local, UploadingDatabase: db, PCS: baidupcs.NewPCSWithClient(1, client), SavePath: "/fixture", Policy: baidupcs.OverWritePolicy, NoRapidUpload: true, taskInfo: &taskframework.TaskInfo{}}
	task.prepareFile()
	if task.Step != StepUploadUpload || task.state == nil || task.state.Uploadid != "initial-fixture-session" {
		t.Fatalf("fixture did not initialize a session: step=%v state=%+v result=%+v", task.Step, task.state, task.prepareResult)
	}
	if requests != 2 {
		t.Fatalf("unexpected initial handshake count: %d", requests)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewUploadingDatabaseAt(database)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	state := reloaded.Search(&local.LocalFileMeta)
	if state == nil || state.Uploadid != "initial-fixture-session" || len(state.BlockList) != 0 {
		t.Fatalf("initial upload session lost before first progress save: %+v", state)
	}
	// A restarted task must reuse the saved session without a new handshake.
	task.UploadingDatabase = reloaded
	task.state = nil
	task.prepareFile()
	if task.Step != StepUploadUpload || task.state == nil || task.state.Uploadid != state.Uploadid || requests != 2 {
		t.Fatal("restart did not reuse the initial upload session")
	}
}
