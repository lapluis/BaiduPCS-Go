package pcsupload

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestChangedResumeWithFreshChunksInitializesSession(t *testing.T) {
	task, _, _ := resumeVerificationFixture(t, true)
	local := task.LocalFileChecksum
	if err := os.WriteFile(local.Path, []byte("zbcdefgh"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, local.ModTimeNS)
	if err := os.Chtimes(local.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := local.Sum(checksum.CHECKSUM_MD5 | checksum.CHECKSUM_SLICE_MD5); err != nil {
		t.Fatal(err)
	}
	if err := local.CalculateChunkedSum(4); err != nil {
		t.Fatal(err)
	}
	task.Context = context.Background()
	task.SavePath, task.Policy = "/fixture", baidupcs.OverWritePolicy
	task.taskInfo = &taskframework.TaskInfo{}
	task.prepareFile()
	if task.prepareResult != nil || task.Step != StepUploadRapidUpload || task.state == nil || task.state.Uploadid != "" {
		t.Fatal("fresh chunks without a matching saved session left a nil session")
	}
}

func TestRapidResumeUsesFreshSourceDigestsBeforePrecreate(t *testing.T) {
	task, _, _ := resumeVerificationFixture(t, true)
	local := task.LocalFileChecksum
	content := []byte("zbcdefgh")
	if err := os.WriteFile(local.Path, content, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, local.ModTimeNS)
	if err := os.Chtimes(local.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	want := md5.Sum(content)
	wantSlice := md5.Sum(content[:4])
	creates := 0
	client := requester.NewHTTPClient()
	client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		payload := `{"error_code":31066}`
		switch {
		case r.URL.Path == "/api/list":
			payload = `{"errno":0,"list":[]}`
		case r.URL.Path == "/api/user/getinfo":
			payload = `{"errno":0,"records":[{"uk":7}]}`
		case r.URL.Path == "/api/precreate":
			creates++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			var blocks []string
			if err := json.Unmarshal([]byte(r.Form.Get("block_list")), &blocks); err != nil || len(blocks) != 1 || blocks[0] != hex.EncodeToString(want[:]) {
				t.Fatal("old chunk hashes used for precreation", blocks, err)
			}
			if r.Form.Get("content-md5") != hex.EncodeToString(want[:]) || r.Form.Get("slice-md5") != hex.EncodeToString(wantSlice[:]) || r.Form.Get("uploadid") != "" {
				t.Fatal("stale whole/slice digest or session sent to precreation")
			}
			payload = `{"errno":0,"return_type":2}`
		case r.URL.Query().Get("method") != "meta":
			t.Error("unexpected request", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})
	task.Context, task.PCS = context.Background(), baidupcs.NewPCSWithClient(1, client)
	task.SavePath, task.Policy = "/fixture", baidupcs.OverWritePolicy
	task.taskInfo, task.UploadStatistic = &taskframework.TaskInfo{}, &UploadStatistic{}
	task.prepareFile()
	if task.prepareResult != nil || task.state == nil || task.state.Uploadid != "" || task.Step != StepUploadRapidUpload {
		t.Fatal("changed source did not prepare a fresh upload")
	}
	next, result := task.rapidUpload()
	if next || !result.Succeed || result.Err != nil || creates != 1 {
		t.Fatalf("fresh rapid response failed: next=%t result=%+v creates=%d", next, result, creates)
	}
}
