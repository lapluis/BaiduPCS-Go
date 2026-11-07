package rc

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

// Entry paths are relative to the requested root; hashes are content MD5 only.
type Entry struct {
	Path      string
	Name      string
	Size      int64
	ModTime   time.Time
	IsDir     bool
	Hash      string `json:"Hash,omitempty"`
	ID        string `json:"-"`
	localInfo os.FileInfo
}

// SameIdentity compares remote IDs and captured local file identities.
func (e Entry) SameIdentity(other Entry) bool {
	return sameIdentity(e, other)
}

type Backend interface {
	Identity() string
	Stat(context.Context, string) (Entry, error)
	List(context.Context, string) ([]Entry, error)
	Hash(context.Context, string) (string, error)
	Mkdir(context.Context, string) error
	Remove(context.Context, string, bool) error
}
type Endpoint struct {
	FS   Backend
	Root string
}

func (e Endpoint) At(rel string) string {
	if _, ok := e.FS.(*Local); ok {
		return filepath.Join(e.Root, filepath.FromSlash(rel))
	}
	return path.Join(e.Root, rel)
}
func (e Endpoint) String() string { return e.FS.Identity() + ":" + e.Root }

type Local struct{}

func (*Local) Identity() string { return "local" }
func (*Local) Stat(ctx context.Context, p string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	f, err := os.Lstat(p)
	if err != nil {
		return Entry{}, err
	}
	if f.Mode()&os.ModeSymlink != 0 || (!f.IsDir() && !f.Mode().IsRegular()) {
		return Entry{}, fmt.Errorf("unsupported special file or symlink: %s", p)
	}
	// Windows loads file IDs lazily for path-based FileInfo. Capture the ID now,
	// rather than accidentally looking up a replacement during a later check.
	if !os.SameFile(f, f) {
		return Entry{}, fmt.Errorf("cannot capture file identity: %s", p)
	}
	return Entry{Name: f.Name(), Size: f.Size(), ModTime: f.ModTime(), IsDir: f.IsDir(), localInfo: f}, nil
}

func sameIdentity(a, b Entry) bool {
	if a.ID != b.ID {
		return false
	}
	if a.localInfo == nil || b.localInfo == nil {
		return a.localInfo == nil && b.localInfo == nil
	}
	return os.SameFile(a.localInfo, b.localInfo)
}
func (l *Local) List(ctx context.Context, p string) ([]Entry, error) {
	return l.list(ctx, p, true)
}

func (l *Local) list(ctx context.Context, p string, hideState bool) ([]Entry, error) {
	// ReadDir follows a linked root. Apply the same metadata validation used
	// for entries before opening the directory itself.
	if _, err := l.Stat(ctx, p); err != nil {
		return nil, err
	}
	ds, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(ds))
	for _, d := range ds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if hideState && d.IsDir() && d.Name() == ".pcsclone-partials" && ownedStageDirectory(filepath.Join(p, d.Name())) {
			continue
		}
		e, err := l.Stat(ctx, filepath.Join(p, d.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Removal checks must see private state too. A preview may ignore children
// whose removals have already been simulated, without hiding real contents.
func directoryEmptyAfterRemovals(ctx context.Context, e Endpoint, rel string, removed map[string]bool) (bool, error) {
	var children []Entry
	var err error
	if local, ok := e.FS.(*Local); ok {
		children, err = local.list(ctx, e.At(rel), false)
	} else {
		children, err = e.FS.List(ctx, e.At(rel))
	}
	if err != nil {
		return false, err
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !removed[path.Join(rel, child.Name)] {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func (l *Local) Hash(ctx context.Context, p string) (string, error) {
	before, err := l.Stat(ctx, p)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before.localInfo, opened) || opened.Size() != before.Size || !opened.ModTime().Equal(before.ModTime) {
		return "", fmt.Errorf("file changed before hashing: %s", p)
	}
	h := md5.New()
	read, err := io.Copy(h, &contextReader{ctx: ctx, r: f})
	if err != nil {
		return "", err
	}
	after, err := l.Stat(ctx, p)
	if err != nil {
		return "", err
	}
	if read != before.Size || !sameIdentity(before, after) || after.Size != before.Size || !after.ModTime.Equal(before.ModTime) {
		return "", fmt.Errorf("file changed while hashing: %s", p)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (l *Local) Mkdir(ctx context.Context, p string) error {
	if err := validLocalPath(p); err != nil {
		return err
	}
	if err := safeParents(p); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.MkdirAll(p, 0755)
}
func (l *Local) Remove(ctx context.Context, p string, dir bool) error {
	e, err := l.Stat(ctx, p)
	if err != nil {
		return err
	}
	if e.IsDir != dir {
		return fmt.Errorf("file type changed: %s", p)
	}
	return os.Remove(p)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func safeParents(p string) error {
	for {
		f, err := os.Lstat(p)
		// Windows junctions can be reported as irregular entries rather than
		// ModeSymlink. Only ordinary files/directories are supported anywhere
		// in a mutation path, matching Local.Stat's metadata policy.
		if err == nil && (f.Mode()&os.ModeSymlink != 0 || (!f.IsDir() && !f.Mode().IsRegular())) {
			return fmt.Errorf("special file or symlink path is not supported: %s", p)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
}

type Remote struct {
	mu  remoteMutex
	PCS *baidupcs.BaiduPCS
	UID uint64
}

func (r *Remote) Identity() string { return fmt.Sprintf("baidu-%d", r.UID) }
func remoteEntry(f *baidupcs.FileDirectory) Entry {
	return Entry{Name: path.Base(f.Path), Size: f.Size, ModTime: time.Unix(f.Mtime, 0), IsDir: f.Isdir, ID: fmt.Sprint(f.FsID)}
}
func remoteError(err pcserror.Error) error {
	if err == nil {
		return nil
	}
	if err.GetErrType() == pcserror.ErrTypeRemoteError && (err.GetRemoteErrCode() == 31066 || err.GetRemoteErrCode() == -9) {
		return os.ErrNotExist
	}
	return err
}

func validateRemoteMetadataPath(requested string, info *baidupcs.FileDirectory) error {
	if info == nil {
		return fmt.Errorf("missing remote metadata for %q", requested)
	}
	if info.Path == "" || path.Clean(info.Path) != path.Clean(requested) {
		return fmt.Errorf("remote metadata path mismatch: requested %q, received %q", requested, info.Path)
	}
	if info.FsID <= 0 {
		return fmt.Errorf("invalid remote file identity for %q", requested)
	}
	return nil
}

func (r *Remote) Stat(ctx context.Context, p string) (Entry, error) {
	if err := r.mu.LockContext(ctx); err != nil {
		return Entry{}, err
	}
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if p == "/" {
		return Entry{Name: "/", IsDir: true}, nil
	}
	f, err := r.PCS.FilesDirectoriesMetaWithContext(ctx, p)
	if err != nil {
		return Entry{}, remoteError(err)
	}
	if err := validateRemoteMetadataPath(p, f); err != nil {
		return Entry{}, err
	}
	return remoteEntry(f), nil
}
func (r *Remote) List(ctx context.Context, p string) ([]Entry, error) {
	if err := r.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fs, err := r.PCS.FilesDirectoriesListWithContext(ctx, p, baidupcs.DefaultOrderOptions)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(fs))
	seen := map[string]bool{}
	for _, f := range fs {
		n := path.Base(f.Path)
		if path.Dir(f.Path) != path.Clean(p) || n == "." || n == ".." || strings.ContainsAny(n, "/\\") || seen[n] {
			return nil, fmt.Errorf("invalid or duplicate directory entry in %s", p)
		}
		seen[n] = true
		out = append(out, remoteEntry(f))
	}
	return out, nil
}

// Baidu's list MD5 is not always a content hash. Hashes are computed through a
// verified download instead of trusting server metadata.
func (r *Remote) Hash(ctx context.Context, p string) (string, error) {
	d, err := os.MkdirTemp("", "pcs-rc-hash-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(d)
	target := filepath.Join(d, "content")
	if err = runTransferWorker(ctx, "download", r.UID, p, target, hashRate(ctx)); err != nil {
		return "", err
	}
	return (&Local{}).Hash(ctx, target)
}
func (r *Remote) Mkdir(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == "/" {
		return nil
	}
	e, err := r.Stat(ctx, p)
	if err == nil {
		if e.IsDir {
			return nil
		}
		return fmt.Errorf("file occupies directory: %s", p)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = r.Mkdir(ctx, path.Dir(p)); err != nil {
		return err
	}
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	err = r.PCS.MkdirWithContext(ctx, p)
	r.mu.Unlock()
	if err != nil {
		current, statErr := r.Stat(ctx, p)
		if statErr == nil && current.IsDir {
			return nil
		}
	}
	return err
}
func (r *Remote) Remove(ctx context.Context, p string, dir bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == "/" {
		return errors.New("refusing to remove remote root")
	}
	e, err := r.Stat(ctx, p)
	if err != nil {
		return err
	}
	if e.IsDir != dir {
		return fmt.Errorf("file type changed: %s", p)
	}
	if dir {
		children, err := r.List(ctx, p)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return fmt.Errorf("directory not empty: %s", p)
		}
		current, err := r.Stat(ctx, p)
		if err != nil {
			return err
		}
		if !current.IsDir || !sameIdentity(e, current) {
			return fmt.Errorf("directory changed during removal checks: %s", p)
		}
	}
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	return r.PCS.RemoveWithContext(ctx, p)
}

type hashRateKey struct{}

func hashRate(ctx context.Context) int64 { rate, _ := ctx.Value(hashRateKey{}).(int64); return rate }
func (r *Remote) copyFile(ctx context.Context, from, to string) error {
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	return r.PCS.CopyWithContext(ctx, &baidupcs.CpMvJSON{From: from, To: to})
}
func (r *Remote) moveFile(ctx context.Context, from, to string) error {
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	return r.PCS.MoveWithContext(ctx, &baidupcs.CpMvJSON{From: from, To: to})
}
