package pcsupload

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func inspectionDatabase(t *testing.T) (*UploadingDatabase, []*checksum.LocalFileMeta) {
	t.Helper()
	root := t.TempDir()
	db, err := NewUploadingDatabaseAt(filepath.Join(root, "resume.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var entries []*checksum.LocalFileMeta
	for i, content := range []string{"abcdefgh", "other-data"} {
		path := filepath.Join(root, []string{"first.bin", "second.bin"}[i])
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum([]byte(content))
		meta := &checksum.LocalFileMeta{Path: path, Length: info.Size(), ModTime: info.ModTime().Unix(), ModTimeNS: info.ModTime().UnixNano(), MD5: sum[:]}
		partSum := md5.Sum([]byte(content[:4]))
		db.UpdateUploading(meta, &uploader.InstanceState{Uploadid: []string{"saved-first", "saved-second"}[i], BlockList: []*uploader.BlockState{{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: hex.EncodeToString(partSum[:])}, {ID: 1, Range: transfer.Range{Begin: 4, End: info.Size()}}}})
		entries = append(entries, meta)
	}
	if err := db.Save(); err != nil {
		t.Fatal(err)
	}
	return db, entries
}

func TestUploadInspectionFailurePreservesOtherProgress(t *testing.T) {
	for _, cause := range []error{os.ErrPermission, errors.New("fixture temporary source I/O failure")} {
		t.Run(cause.Error(), func(t *testing.T) {
			db, entries := inspectionDatabase(t)
			previous := statUploadSource
			defer func() { statUploadSource = previous }()
			failedProbes := 0
			statUploadSource = func(path string) (os.FileInfo, error) {
				if path == entries[0].Path {
					failedProbes++
					return nil, &os.PathError{Op: "stat", Path: path, Err: cause}
				}
				return previous(path)
			}
			failedQuery := &checksum.LocalFileMeta{Path: entries[0].Path, Length: entries[0].Length}
			if state := db.Search(failedQuery); state != nil {
				t.Error("unverified state returned")
			}
			if failedQuery.MD5 != nil || failedQuery.BlocksList != nil {
				t.Error("failed lookup populated cached metadata")
			}
			healthy := db.Search(&checksum.LocalFileMeta{Path: entries[1].Path, Length: entries[1].Length})
			if healthy == nil || healthy.Uploadid != "saved-second" {
				t.Fatal("unrelated lookup was blocked")
			}
			if failedProbes != 1 {
				t.Errorf("unrelated lookup inspected unavailable source: probes=%d", failedProbes)
			}
			if err := db.Save(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(db.filename)
			if err != nil {
				t.Fatal(err)
			}
			var saved UploadingDatabase
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.UploadingList) != 2 {
				t.Errorf("transient inspection discarded progress: records=%d", len(saved.UploadingList))
			}
			statUploadSource = previous
			recovered := db.Search(&checksum.LocalFileMeta{Path: entries[0].Path, Length: entries[0].Length})
			if recovered == nil || recovered.Uploadid != "saved-first" || recovered.BlockList[0].CheckSum == "" {
				t.Error("existing progress did not recover after I/O fault cleared")
			}
		})
	}
}

func TestUploadPreparationStopsOnResumeInspectionFailure(t *testing.T) {
	db, entries := inspectionDatabase(t)
	local := checksum.NewLocalFileChecksum(entries[0].Path, 4)
	if err := local.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	cause := errors.New("fixture resume source metadata failure")
	previous := statUploadSource
	defer func() { statUploadSource = previous }()
	statUploadSource = func(path string) (os.FileInfo, error) {
		if path == entries[0].Path {
			return nil, &os.PathError{Op: "stat", Path: path, Err: cause}
		}
		return previous(path)
	}
	calls := 0
	client := requester.NewHTTPClient()
	client.Client.Transport = uploadRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected remote request")
	})
	task := &UploadTaskUnit{PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, UploadingDatabase: db, SavePath: "/fixture", NoRapidUpload: true, Policy: baidupcs.OverWritePolicy, taskInfo: &taskframework.TaskInfo{}}
	before, err := os.ReadFile(db.filename)
	if err != nil {
		t.Fatal(err)
	}
	result := task.Run()
	if task.Step != JustGoon || result == nil || !errors.Is(result.Err, cause) || result.Succeed || calls != 0 {
		t.Errorf("inspection failure did not stop preparation: step=%v result=%+v calls=%d", task.Step, result, calls)
	}
	if len(db.UploadingList) != 2 {
		t.Error("preparation discarded resume state")
	}
	if data, err := os.ReadFile(db.filename); err != nil || string(data) != string(before) {
		t.Error("failed preparation changed persisted progress")
	}
	statUploadSource = previous
	if err := local.OpenPath(); err != nil {
		t.Fatal(err)
	}
	task.prepareResult = nil
	task.Step = StepUploadRapidUpload
	task.prepareFile()
	if task.Step != StepUploadUpload || task.state == nil || task.state.Uploadid != "saved-first" || calls != 0 {
		t.Error("retry failed to reuse saved upload session")
	}
}

func TestUploadLookupReportsInspectionErrorAndAcceptsVerifiedDigest(t *testing.T) {
	db, entries := inspectionDatabase(t)
	previous := statUploadSource
	defer func() { statUploadSource = previous }()
	statUploadSource = func(path string) (os.FileInfo, error) {
		if path == entries[0].Path {
			return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
		}
		return previous(path)
	}
	query := &checksum.LocalFileMeta{Path: entries[0].Path, Length: entries[0].Length}
	state, err := db.SearchWithError(query)
	if state != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unknown state treated as a cache miss: state=%v err=%v", state, err)
	}
	if query.MD5 != nil || query.BlocksList != nil || len(db.UploadingList) != 2 {
		t.Fatal("failed lookup changed metadata or removed progress")
	}
	query.MD5 = append([]byte(nil), entries[0].MD5...)
	state, err = db.SearchWithError(query)
	if err != nil || state == nil || state.Uploadid != "saved-first" {
		t.Fatalf("verified current contents could not resume: %v %v", state, err)
	}
	state.Uploadid = "caller mutation"
	query = &checksum.LocalFileMeta{Path: entries[0].Path, Length: entries[0].Length, MD5: entries[0].MD5}
	if state, err = db.SearchWithError(query); err != nil || state == nil || state.Uploadid != "saved-first" {
		t.Fatal("lookup returned shared mutable state")
	}
}

func TestUploadLookupStillDropsMissingAndChangedSources(t *testing.T) {
	for _, kind := range []string{"missing", "changed"} {
		t.Run(kind, func(t *testing.T) {
			db, entries := inspectionDatabase(t)
			if kind == "missing" {
				if err := os.Remove(entries[0].Path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(entries[0].Path, []byte("changed source size"), 0600); err != nil {
				t.Fatal(err)
			}
			healthy, err := db.SearchWithError(&checksum.LocalFileMeta{Path: entries[1].Path, Length: entries[1].Length})
			if err != nil || healthy == nil || len(db.UploadingList) != 2 {
				t.Fatal("unrelated lookup removed another file's record")
			}
			query := &checksum.LocalFileMeta{Path: entries[0].Path, Length: entries[0].Length}
			state, err := db.SearchWithError(query)
			if err != nil || state != nil || len(db.UploadingList) != 1 {
				t.Fatalf("confirmed stale source retained: state=%v err=%v records=%d", state, err, len(db.UploadingList))
			}
			healthy, err = db.SearchWithError(&checksum.LocalFileMeta{Path: entries[1].Path, Length: entries[1].Length})
			if err != nil || healthy == nil || healthy.Uploadid != "saved-second" {
				t.Fatal("unrelated healthy progress changed")
			}
		})
	}
}

func TestUploadLookupCurrentContentsInvalidateOldState(t *testing.T) {
	for _, kind := range []string{"size", "digest"} {
		t.Run(kind, func(t *testing.T) {
			db, entries := inspectionDatabase(t)
			content := []byte("changed source size")
			if kind == "digest" {
				content = []byte("xxxxxxxx")
			}
			if err := os.WriteFile(entries[0].Path, content, 0600); err != nil {
				t.Fatal(err)
			}
			query := &checksum.LocalFileMeta{Path: entries[0].Path, Length: int64(len(content))}
			if kind == "digest" {
				sum := md5.Sum(content)
				query.MD5 = sum[:]
			}
			previous := statUploadSource
			defer func() { statUploadSource = previous }()
			probes := 0
			statUploadSource = func(path string) (os.FileInfo, error) {
				probes++
				return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
			}
			state, err := db.SearchWithError(query)
			if err != nil || state != nil || len(db.UploadingList) != 1 || probes != 0 {
				t.Fatalf("known content change did not invalidate old progress: state=%v err=%v records=%d probes=%d", state, err, len(db.UploadingList), probes)
			}
		})
	}
}
