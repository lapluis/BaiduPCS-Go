package rc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var errResumeIdentity = errors.New("download entity changed; partial data must be restarted")

const stageMarker = "pcsclone-owned-download-state-v1\n"

var stageDirectoryMu sync.Mutex

func ownedStageDirectory(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".owner"))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, ".owner"))
	return err == nil && string(b) == stageMarker
}
func prepareStageDirectory(dir string) error {
	stageDirectoryMu.Lock()
	defer stageDirectoryMu.Unlock()
	if err := safeParents(dir); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		if !os.IsExist(err) {
			return err
		}
		if !ownedStageDirectory(dir) {
			return fmt.Errorf("download state directory is not owned by pcsclone: %s", dir)
		}
		return nil
	}
	return os.WriteFile(filepath.Join(dir, ".owner"), []byte(stageMarker), 0600)
}

type downloadRecord struct {
	Version                  int
	Account                  uint64
	Source, Destination, ID  string
	Size, Modified           int64
	LocalSize, LocalModified int64
	ETag, LastModified       string
}
type downloadStage struct {
	payload, record string
	info            downloadRecord
}

func downloadBase(uid uint64, source, destination string) (string, string, error) {
	abs, err := filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", uid, source, abs)))
	return filepath.Join(filepath.Dir(abs), ".pcsclone-partials", fmt.Sprintf("%x", key)), abs, nil
}

func stageFor(remote *Remote, source, destination string, entry Entry) (downloadStage, error) {
	base, abs, err := downloadBase(remote.UID, source, destination)
	if err != nil {
		return downloadStage{}, err
	}
	dir := filepath.Dir(base)
	if err := prepareStageDirectory(dir); err != nil {
		return downloadStage{}, err
	}
	s := downloadStage{payload: base + ".part", record: base + ".json", info: downloadRecord{Version: 1, Account: remote.UID, Source: source, Destination: abs, ID: entry.ID, Size: entry.Size, Modified: entry.ModTime.UnixNano()}}
	for _, p := range []string{s.payload, s.record, s.payload + pcsdownload.DownloadSuffix} {
		if info, e := os.Lstat(p); e == nil && !info.Mode().IsRegular() {
			return s, fmt.Errorf("unsafe download state file: %s", p)
		} else if e != nil && !os.IsNotExist(e) {
			return s, e
		}
	}
	var old downloadRecord
	b, readErr := os.ReadFile(s.record)
	valid := readErr == nil && json.Unmarshal(b, &old) == nil && old.Version == 1 && old.Account == s.info.Account && old.Source == source && old.Destination == abs && old.ID == entry.ID && old.ID != "" && old.ID != "0" && old.Size == entry.Size && old.Modified == s.info.Modified && (old.ETag != "" || old.LastModified != "")
	if strings.HasPrefix(old.ETag, "W/") {
		valid = false
	}
	if f, e := os.Stat(s.payload); e != nil || f.Size() != old.LocalSize || f.ModTime().UnixNano() != old.LocalModified {
		valid = false
	}
	if _, e := os.Stat(s.payload + pcsdownload.DownloadSuffix); e != nil {
		valid = false
	}
	if valid {
		s.info = old
		return s, nil
	}
	if err := s.reset(); err != nil {
		return s, err
	}
	return s, s.save()
}
func (s *downloadStage) reset() error {
	for _, p := range []string{s.payload, s.payload + pcsdownload.DownloadSuffix, s.record} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.info.ETag = ""
	s.info.LastModified = ""
	s.info.LocalSize = 0
	s.info.LocalModified = 0
	return nil
}
func (s *downloadStage) save() error {
	if info, err := os.Stat(s.payload); err == nil {
		s.info.LocalSize = info.Size()
		s.info.LocalModified = info.ModTime().UnixNano()
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := json.Marshal(s.info)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.record), ".state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, s.record)
}
func (s *downloadStage) cleanup() {
	_ = os.Remove(s.record)
	_ = os.Remove(s.payload + pcsdownload.DownloadSuffix)
}

type completedDownload struct {
	Source downloadRecord
	SHA256 string
}

// Version 1 hashed the published destination. Version 2 bound the digest to
// the payload but could certify zero-filled tails after a premature range EOF.
// Version 3 could lose unfinished ranges when reassigning canceled workers.
// Revalidate those old records through full content comparison.
const completedDownloadVersion = 4

func hashSHA256(ctx context.Context, name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, &contextReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func (s *downloadStage) canRecordCompletion() bool {
	return s.info.ID != "" && s.info.ID != "0" && s.info.ETag != "" && !strings.HasPrefix(s.info.ETag, "W/")
}

func (s *downloadStage) complete(ctx context.Context, name, payloadHash string) error {
	if !s.canRecordCompletion() || payloadHash == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	copy := *s
	copy.info.Version = completedDownloadVersion
	copy.info.LocalSize = info.Size()
	copy.info.LocalModified = info.ModTime().UnixNano()
	b, err := json.Marshal(completedDownload{Source: copy.info, SHA256: payloadHash})
	if err != nil {
		return err
	}
	if err := safeParents(s.record + ".done"); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.record), ".complete-*")
	if err != nil {
		return err
	}
	nameTmp := f.Name()
	defer os.Remove(nameTmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(nameTmp, s.record+".done")
}
func verifiedDownloadedFile(ctx context.Context, remote *Remote, source, destination string, a, b Entry) (bool, error) {
	base, abs, err := downloadBase(remote.UID, source, destination)
	if err != nil {
		return false, err
	}
	if err := safeParents(base + ".json.done"); err != nil {
		return false, err
	}
	data, err := os.ReadFile(base + ".json.done")
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var done completedDownload
	if json.Unmarshal(data, &done) != nil {
		return false, nil
	}
	v := done.Source
	if v.Version != completedDownloadVersion || v.Account != remote.UID || v.Source != source || v.Destination != abs || v.ID == "" || v.ID == "0" || v.ID != a.ID || v.Size != a.Size || v.Modified != a.ModTime.UnixNano() || v.LocalSize != b.Size || v.LocalModified != b.ModTime.UnixNano() || v.ETag == "" {
		return false, nil
	}
	hash, err := hashSHA256(ctx, destination)
	if err != nil || hash != done.SHA256 {
		return false, err
	}
	// A saved file ID and second-resolution mtime alone cannot prove that
	// remote contents are unchanged. Revalidate the live HTTP entity as well.
	var current transferProgress
	probeCtx := context.WithValue(ctx, progressKey{}, func(p transferProgress) { current = p })
	err = runTransferWorker(probeCtx, "probe", remote.UID, source, "", 0)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err == nil && current.Total == a.Size && current.ETag == v.ETag, nil
}
