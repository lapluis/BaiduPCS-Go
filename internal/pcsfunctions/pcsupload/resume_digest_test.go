package pcsupload

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type digestResumeBackend struct {
	parts  map[int]string
	merged bool
}

func (*digestResumeBackend) Precreate(context.Context) (string, pcserror.Error) {
	return "fixture", nil
}
func (b *digestResumeBackend) TmpFile(_ context.Context, _, _ string, id int, _ int64, r rio.ReaderLen64) (string, error) {
	data, err := io.ReadAll(r)
	b.parts[id] = string(data)
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:]), err
}
func (b *digestResumeBackend) CreateSuperFile(_ context.Context, _, _, _ string, _ int64, sums map[int]string) error {
	for id, data := range []string{"abcd", "efgh"} {
		sum := md5.Sum([]byte(data))
		if !strings.EqualFold(sums[id], hex.EncodeToString(sum[:])) {
			return fmt.Errorf("invalid restored checksum for block %d", id)
		}
	}
	b.merged = true
	return nil
}

func TestLegacyInvalidUploadDigestIsReuploaded(t *testing.T) {
	first, second := md5.Sum([]byte("abcd")), md5.Sum([]byte("efgh"))
	validFirst, validSecond := hex.EncodeToString(first[:]), hex.EncodeToString(second[:])
	for _, tc := range []struct {
		name, digest string
		reupload     bool
	}{
		{"short", "abc", true},
		{"long", validFirst + "0", true},
		{"non-hex", strings.Repeat("z", 32), true},
		{"whitespace", strings.Repeat(" ", 32), true},
		{"pending", "", true},
		{"valid", validFirst, false},
		{"uppercase", strings.ToUpper(validFirst), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source, database := filepath.Join(root, "file"), filepath.Join(root, "resume.json")
			if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
				t.Fatal(err)
			}
			meta := &checksum.LocalFileMeta{Path: source, Length: 8, ModTime: -1}
			original, err := json.Marshal(&UploadingDatabase{UploadingList: []*Uploading{{LocalFileMeta: meta, State: &uploader.InstanceState{Uploadid: "fixture", BlockList: []*uploader.BlockState{
				{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: tc.digest},
				{ID: 1, Range: transfer.Range{Begin: 4, End: 8}, CheckSum: validSecond},
			}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(database, original, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := NewUploadingDatabaseAt(database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			state := db.Search(meta)
			if state == nil || state.Uploadid != "fixture" || state.BlockList[1].CheckSum != validSecond {
				t.Fatal("valid portion of the resume state was lost")
			}
			f, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			backend := &digestResumeBackend{parts: make(map[int]string)}
			u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(f), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			u.SetInstanceState(state)
			success := false
			u.OnSuccess(func() { success = true })
			u.Execute()
			wantParts := 0
			if tc.reupload {
				wantParts = 1
			}
			if !success || !backend.merged || len(backend.parts) != wantParts || (tc.reupload && backend.parts[0] != "abcd") {
				t.Fatalf("legacy digest was not repaired: success=%v merged=%v uploaded=%v", success, backend.merged, backend.parts)
			}
			data, err := os.ReadFile(database)
			if err != nil || string(data) != string(original) {
				t.Fatal("reading resume state rewrote the database", err)
			}
		})
	}
}
