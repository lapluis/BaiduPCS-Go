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

var readDownloadRecord = readDownloadRecordFile
var statDownloadState = os.Stat
var removeDownloadState = os.Remove

// These records contain scalar metadata, not the per-range checkpoint. Use the
// downloader checkpoint's size budget while allowing ample room for paths and
// HTTP entity validators. Inspection errors must preserve existing progress.
const maxDownloadRecordSize = 16 << 20

var errDownloadRecordSize = errors.New("download record size exceeds limit")

func readDownloadRecordFile(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("download record is not a regular file: %s", name)
	}
	if info.Size() < 0 || info.Size() > maxDownloadRecordSize {
		return nil, fmt.Errorf("%w: %s", errDownloadRecordSize, name)
	}
	data, err := readDownloadRecordContents(f)
	if err != nil {
		return nil, fmt.Errorf("read download record %s: %w", name, err)
	}
	return data, nil
}

func readDownloadRecordContents(r io.Reader) ([]byte, error) {
	// A file can grow after Stat. Bound the actual read too, leaving one byte
	// to distinguish an exact-budget record from a truncated oversized record.
	data, err := io.ReadAll(io.LimitReader(r, maxDownloadRecordSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownloadRecordSize {
		return nil, errDownloadRecordSize
	}
	return data, nil
}

func ownedStageDirectory(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".owner"))
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(stageMarker)) {
		return false
	}
	f, err := os.Open(filepath.Join(dir, ".owner"))
	if err != nil {
		return false
	}
	defer f.Close()
	// The file may grow or be replaced after Lstat. Read at most one byte
	// beyond the fixed marker so a user file cannot cause an unbounded buffer.
	b, err := io.ReadAll(io.LimitReader(f, int64(len(stageMarker))+1))
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

func stageFor(ctx context.Context, remote *Remote, source, destination string, entry Entry) (stage downloadStage, stageErr error) {
	defer func() { stageErr = preserveContextError(ctx, stageErr) }()
	if err := ctx.Err(); err != nil {
		return downloadStage{}, err
	}
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
	b, readErr := readDownloadRecord(s.record)
	// An unreadable checkpoint is not evidence of a stale entity. Preserve all
	// files on inspection failures so a transient lock or I/O error is retryable.
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return s, fmt.Errorf("read download checkpoint: %w", readErr)
	}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	valid := readErr == nil && json.Unmarshal(b, &old) == nil && old.Version == 1 && old.Account == s.info.Account && old.Source == source && old.Destination == abs && old.ID == entry.ID && old.ID != "" && old.ID != "0" && old.Size == entry.Size && old.Modified == s.info.Modified && (old.ETag != "" || old.LastModified != "")
	if strings.HasPrefix(old.ETag, "W/") {
		valid = false
	}
	f, statErr := statDownloadState(s.payload)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return s, fmt.Errorf("inspect partial download: %w", statErr)
	}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	if statErr != nil || f.Size() != old.LocalSize || f.ModTime().UnixNano() != old.LocalModified {
		valid = false
	}
	if _, e := statDownloadState(s.payload + pcsdownload.DownloadSuffix); e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			return s, fmt.Errorf("inspect download ranges: %w", e)
		}
		valid = false
	}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	if valid {
		if err := detachSharedPartial(ctx, s.payload, f); err != nil {
			return s, fmt.Errorf("prepare shared download partial: %w", err)
		}
		s.info = old
		return s, nil
	}
	if err := s.reset(); err != nil {
		return s, err
	}
	return s, s.save()
}

// A resumed payload is written in place. Give it an independent inode before
// handing it to a worker, preserving both progress and any external hard links.
func detachSharedPartial(ctx context.Context, path string, before os.FileInfo) error {
	shared, err := fileHasMultipleLinks(path, before)
	if err != nil || !shared {
		return err
	}
	if !os.SameFile(before, before) {
		return errors.New("cannot capture partial file identity")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".detach-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Close(); err != nil {
		return err
	}
	// Local preparation does not count as newly downloaded network bytes.
	copyCtx := context.WithValue(ctx, progressKey{}, func(transferProgress) {})
	if err := copyLocal(copyCtx, path, name, 0); err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("partial download changed while preparing an independent copy")
	}
	cloned, err := os.Stat(name)
	if err != nil {
		return err
	}
	if cloned.Size() != before.Size() {
		return io.ErrUnexpectedEOF
	}
	if err := os.Chtimes(name, before.ModTime(), before.ModTime()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (s *downloadStage) reset() (resetErr error) {
	defer func() {
		if resetErr != nil {
			resetErr = fmt.Errorf("%w: reset download partial: %w", ErrCheckpointSave, resetErr)
		}
	}()
	for _, p := range []string{s.payload, s.payload + pcsdownload.DownloadSuffix, s.record} {
		if err := removeDownloadState(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.info.ETag = ""
	s.info.LastModified = ""
	s.info.LocalSize = 0
	s.info.LocalModified = 0
	return nil
}
func (s *downloadStage) save() (saveErr error) {
	defer func() {
		if saveErr != nil {
			saveErr = fmt.Errorf("%w: %w", ErrCheckpointSave, saveErr)
		}
	}()
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
	if len(b) > maxDownloadRecordSize {
		return errDownloadRecordSize
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
	if len(b) > maxDownloadRecordSize {
		return errDownloadRecordSize
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
	data, err := readDownloadRecordFile(base + ".json.done")
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
	if cancelErr := ctx.Err(); cancelErr != nil {
		return false, errors.Join(cancelErr, err)
	}
	// A cache miss can fall back to a full comparison. Failures observing the
	// worker or saving its state must remain visible and must not start more I/O.
	if errors.Is(err, ErrCheckpointSave) || errors.Is(err, ErrWorkerProtocol) || errors.Is(err, ErrProgressCallback) || errors.Is(err, ErrLogCallback) {
		return false, err
	}
	return err == nil && current.Total == a.Size && current.ETag == v.ETag, nil
}
