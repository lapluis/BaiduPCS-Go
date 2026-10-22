package rc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type Options struct {
	ShowProgress                                       bool
	DryRun, Checksum, SizeOnly, IgnoreExisting, Update bool
	CreateEmptyDirs, DeleteEmptyDirs, DeleteExcluded   bool
	Transfers, Checkers, Retries, MaxDelete            int
	BWLimit                                            int64
	Filter                                             Filter
	Emit                                               func(string, string)
}
type Action struct {
	Kind, Path, Reason  string
	Source, Destination *Entry
}
type Plan struct {
	Source, Destination Endpoint
	Actions             []Action
	Move                bool
	SourceDirs          []string
	SourceSnapshot      map[string]Entry
}
type Result struct {
	Copied, Skipped, Deleted, Failed int
	Bytes                            int64
}

func snapshot(ctx context.Context, e Endpoint) (map[string]Entry, error) {
	root, err := e.FS.Stat(ctx, e.Root)
	if err != nil {
		return nil, err
	}
	if !root.IsDir {
		return nil, errors.New("directory expected")
	}
	out := map[string]Entry{}
	var walk func(string) error
	walk = func(rel string) error {
		items, err := e.FS.List(ctx, e.At(rel))
		if err != nil {
			return err
		}
		for _, v := range items {
			if v.Name == "" || v.Name == "." || v.Name == ".." || strings.ContainsAny(v.Name, "/\\") {
				return fmt.Errorf("unsafe entry name %q", v.Name)
			}
			v.Path = path.Join(rel, v.Name)
			if _, ok := out[v.Path]; ok {
				return fmt.Errorf("duplicate path %s", v.Path)
			}
			out[v.Path] = v
			if v.IsDir {
				if err = walk(v.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err = walk("")
	return out, err
}
func sortedKeys(m map[string]Entry) []string {
	keys := make([]string, 0, len(m))
	for p := range m {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	return keys
}
func validatePair(a, b Endpoint) error {
	if a.FS.Identity() != b.FS.Identity() {
		return nil
	}
	x, y := strings.TrimRight(filepath.ToSlash(a.Root), "/"), strings.TrimRight(filepath.ToSlash(b.Root), "/")
	if a.FS.Identity() == "local" && runtime.GOOS == "windows" {
		x = strings.ToLower(x)
		y = strings.ToLower(y)
	}
	if x == y || strings.HasPrefix(x, y+"/") || strings.HasPrefix(y, x+"/") {
		return errors.New("source and destination overlap")
	}
	return nil
}
func validLocalName(p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	for _, n := range strings.Split(p, "/") {
		if strings.ContainsAny(n, `<>:"\|?*`) || strings.TrimRight(n, ". ") != n {
			return fmt.Errorf("unsupported Windows filename %q", n)
		}
		base := strings.ToUpper(strings.SplitN(n, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return fmt.Errorf("reserved Windows filename %q", n)
		}
	}
	return nil
}
func same(ctx context.Context, src, dst Endpoint, p string, a, b Entry, o Options) (bool, error) {
	if a.Size != b.Size {
		return false, nil
	}
	if o.SizeOnly {
		return true, nil
	}
	n := o.Checkers
	if n < 1 {
		n = 1
	}
	rate := o.BWLimit / int64(n)
	if o.BWLimit > 0 && rate < 1 {
		rate = 1
	}
	ctx = context.WithValue(ctx, hashRateKey{}, rate)
	x, err := src.FS.Hash(ctx, src.At(p))
	if err != nil {
		return false, err
	}
	y, err := dst.FS.Hash(ctx, dst.At(p))
	return x == y, err
}
func BuildPlan(ctx context.Context, src, dst Endpoint, mode string, o Options) (*Plan, error) {
	if err := validatePair(src, dst); err != nil {
		return nil, err
	}
	sm, err := snapshot(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("source scan: %w", err)
	}
	dm := map[string]Entry{}
	_, statErr := dst.FS.Stat(ctx, dst.Root)
	if statErr == nil {
		dm, err = snapshot(ctx, dst)
		if err != nil {
			return nil, fmt.Errorf("destination scan: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	p := &Plan{Source: src, Destination: dst, Move: mode == "move", SourceSnapshot: sm}
	if errors.Is(statErr, os.ErrNotExist) {
		p.Actions = append(p.Actions, Action{Kind: "mkdir", Path: ""})
	}
	seen := map[string]string{}
	if _, local := dst.FS.(*Local); local && runtime.GOOS == "windows" {
		names := map[string]string{}
		for _, inventory := range []map[string]Entry{dm, sm} {
			for name := range inventory {
				folded := strings.ToLower(name)
				if prior, ok := names[folded]; ok && prior != name {
					return nil, fmt.Errorf("case collision: %s and %s", prior, name)
				}
				names[folded] = name
			}
		}
	}
	var candidates []Action
	for _, k := range sortedKeys(sm) {
		s := sm[k]
		d, exists := dm[k]
		if s.IsDir {
			if exists && !d.IsDir {
				return nil, fmt.Errorf("file/directory conflict: %s", k)
			}
			p.SourceDirs = append(p.SourceDirs, k)
			if o.CreateEmptyDirs && o.Filter.Match(k) {
				if exists && !d.IsDir {
					return nil, fmt.Errorf("file/directory conflict: %s", k)
				}
				if !exists {
					p.Actions = append(p.Actions, Action{Kind: "mkdir", Path: k})
				}
			}
			continue
		}
		if !o.Filter.Match(k) {
			continue
		}
		if _, local := dst.FS.(*Local); local {
			if err = validLocalName(k); err != nil {
				return nil, err
			}
			fold := strings.ToLower(k)
			if prev, ok := seen[fold]; ok && prev != k {
				return nil, fmt.Errorf("case collision: %s and %s", prev, k)
			}
			seen[fold] = k
		}
		for parent := path.Dir(k); parent != "."; parent = path.Dir(parent) {
			if d, ok := dm[parent]; ok && !d.IsDir {
				return nil, fmt.Errorf("file blocks target directory: %s", parent)
			}
		}
		a := Action{Kind: "copy", Path: k, Source: &s}
		if exists {
			if d.IsDir {
				return nil, fmt.Errorf("file/directory conflict: %s", k)
			}
			a.Destination = &d
			if o.IgnoreExisting {
				a.Kind = "skip"
				a.Reason = "ignore-existing"
			} else if o.Update && d.ModTime.After(s.ModTime) {
				a.Kind = "skip"
				a.Reason = "destination-newer"
			}
		}
		candidates = append(candidates, a)
	}
	// Bounded parallel comparison. No mutations occur until every comparison succeeds.
	n := o.Checkers
	if n < 1 {
		n = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var compareErr error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				a := &candidates[i]
				if a.Kind != "copy" || a.Destination == nil {
					continue
				}
				equal, e := same(ctx, src, dst, a.Path, *a.Source, *a.Destination, o)
				if e != nil {
					mu.Lock()
					compareErr = errors.Join(compareErr, fmt.Errorf("compare %s: %w", a.Path, e))
					mu.Unlock()
				} else if equal {
					a.Kind = "skip"
					a.Reason = "identical"
				}
			}
		}()
	}
	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if compareErr != nil {
		return nil, compareErr
	}
	p.Actions = append(p.Actions, candidates...)
	if mode == "sync" {
		count := 0
		for _, k := range sortedKeys(dm) {
			d := dm[k]
			if _, exists := sm[k]; d.IsDir || (exists && (!o.DeleteExcluded || o.Filter.Match(k))) {
				continue
			}
			if !o.DeleteExcluded && !o.Filter.Match(k) {
				continue
			}
			p.Actions = append(p.Actions, Action{Kind: "delete", Path: k, Destination: &d})
			count++
		}
		dirs := sortedKeys(dm)
		sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
		for _, k := range dirs {
			d := dm[k]
			if _, exists := sm[k]; d.IsDir && !exists && (o.DeleteExcluded || o.Filter.Match(k)) {
				p.Actions = append(p.Actions, Action{Kind: "rmdir", Path: k, Destination: &d})
			}
		}
		if o.MaxDelete >= 0 && count > o.MaxDelete {
			return nil, fmt.Errorf("planned deletions %d exceed --max-delete %d", count, o.MaxDelete)
		}
	}
	return p, nil
}
func unchanged(ctx context.Context, e Endpoint, p string, old *Entry) error {
	current, err := e.FS.Stat(ctx, e.At(p))
	if old == nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("destination appeared after planning: %s", p)
	}
	if err != nil {
		return err
	}
	if current.Size != old.Size || current.IsDir != old.IsDir || !current.ModTime.Equal(old.ModTime) || current.ID != old.ID {
		return fmt.Errorf("file changed after planning: %s", p)
	}
	return nil
}
func actionPath(p *Plan, a Action) string {
	if a.Path != "" {
		return a.Path
	}
	return p.Source.Root + " -> " + p.Destination.Root
}
func logAction(o Options, kind, p string) {
	if o.Emit != nil {
		o.Emit(kind, p)
	}
}
func Execute(ctx context.Context, p *Plan, o Options) (Result, error) {
	result := Result{}
	if o.DryRun {
		for _, a := range p.Actions {
			logAction(o, "would-"+a.Kind, actionPath(p, a))
			if p.Move && (a.Kind == "copy" || a.Reason == "identical") {
				logAction(o, "would-remove-source", actionPath(p, a))
			}
		}
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	for _, a := range p.Actions {
		if a.Kind == "mkdir" {
			if err := p.Destination.FS.Mkdir(ctx, p.Destination.At(a.Path)); err != nil {
				return result, err
			}
		}
	}
	n := o.Transfers
	if n < 1 {
		n = 1
	}
	jobs := make(chan Action)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs error
	active := 0
	if o.ShowProgress {
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					mu.Lock()
					text := fmt.Sprintf("active=%d copied=%d skipped=%d failed=%d completed-bytes=%d", active, result.Copied, result.Skipped, result.Failed, result.Bytes)
					mu.Unlock()
					logAction(o, "progress", text)
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				mu.Lock()
				active++
				mu.Unlock()
				var err error
				if a.Kind == "skip" {
					mu.Lock()
					result.Skipped++
					mu.Unlock()
					if p.Move && a.Reason == "identical" {
						err = verifyAndRemove(ctx, p.Source, p.Destination, a, o)
					}
				} else {
					serverMoved := false
					if p.Move {
						serverMoved, err = tryServerMove(ctx, p.Source, p.Destination, a, o)
					}
					for attempt := 0; !serverMoved && attempt <= o.Retries; attempt++ {
						err = copyAction(ctx, p.Source, p.Destination, a, o)
						if err == nil || ctx.Err() != nil {
							break
						}
						if attempt < o.Retries {
							select {
							case <-ctx.Done():
								err = ctx.Err()
							case <-time.After(time.Duration(attempt+1) * time.Second):
							}
						}
					}
					if err == nil && p.Move && !serverMoved {
						err = verifyAndRemove(ctx, p.Source, p.Destination, a, o)
					}
					if err == nil {
						mu.Lock()
						result.Copied++
						result.Bytes += a.Source.Size
						mu.Unlock()
					}
				}
				mu.Lock()
				active--
				if err != nil {
					result.Failed++
					errs = errors.Join(errs, fmt.Errorf("%s: %w", a.Path, err))
					logAction(o, "error", actionPath(p, a))
				} else {
					logAction(o, a.Kind, actionPath(p, a))
				}
				mu.Unlock()
			}
		}()
	}
	for _, a := range p.Actions {
		if a.Kind == "copy" || a.Kind == "skip" {
			jobs <- a
		}
	}
	close(jobs)
	wg.Wait()
	if errs != nil {
		return result, errs
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Refuse sync deletion if the source inventory changed after planning.
	hasDelete := false
	for _, a := range p.Actions {
		if a.Kind == "delete" || a.Kind == "rmdir" {
			hasDelete = true
		}
	}
	if hasDelete && p.SourceSnapshot != nil {
		now, err := snapshot(ctx, p.Source)
		if err != nil {
			return result, err
		}
		if len(now) != len(p.SourceSnapshot) {
			return result, errors.New("source tree changed; deletion blocked")
		}
		for k, old := range p.SourceSnapshot {
			cur, ok := now[k]
			if !ok || cur.IsDir != old.IsDir || (!cur.IsDir && (cur.Size != old.Size || !cur.ModTime.Equal(old.ModTime) || cur.ID != old.ID)) {
				return result, errors.New("source tree changed; deletion blocked")
			}
		}
	}
	// All deletion candidates are rechecked before the first delete.
	for _, a := range p.Actions {
		if a.Kind == "delete" {
			if err := unchanged(ctx, p.Destination, a.Path, a.Destination); err != nil {
				return result, err
			}
		}
	}
	for _, a := range p.Actions {
		if a.Kind == "delete" {
			if err := p.Destination.FS.Remove(ctx, p.Destination.At(a.Path), false); err != nil {
				return result, err
			}
			result.Deleted++
			logAction(o, "delete", a.Path)
		}
	}
	for _, a := range p.Actions {
		if a.Kind == "rmdir" {
			children, err := p.Destination.FS.List(ctx, p.Destination.At(a.Path))
			if err != nil {
				return result, err
			}
			if len(children) == 0 {
				if err = p.Destination.FS.Remove(ctx, p.Destination.At(a.Path), true); err != nil {
					return result, err
				}
				logAction(o, "rmdir", a.Path)
			}
		}
	}
	if p.Move && o.DeleteEmptyDirs {
		if err := removeEmpty(ctx, p.Source, false, o); err != nil {
			return result, err
		}
	}
	return result, nil
}
func verifyAndRemove(ctx context.Context, src, dst Endpoint, a Action, o Options) error {
	ctx = context.WithValue(ctx, hashRateKey{}, perTransferRate(o))
	if err := unchanged(ctx, src, a.Path, a.Source); err != nil {
		return err
	}
	// Never let --size-only, --update or --ignore-existing authorize data loss.
	x, err := src.FS.Hash(ctx, src.At(a.Path))
	if err != nil {
		return err
	}
	y, err := dst.FS.Hash(ctx, dst.At(a.Path))
	if err != nil {
		return err
	}
	if x != y {
		return errors.New("content verification failed; source retained")
	}
	if err = unchanged(ctx, src, a.Path, a.Source); err != nil {
		return err
	}
	return src.FS.Remove(ctx, src.At(a.Path), false)
}
func copyAction(ctx context.Context, src, dst Endpoint, a Action, o Options) error {
	ctx = context.WithValue(ctx, hashRateKey{}, perTransferRate(o))
	if err := unchanged(ctx, src, a.Path, a.Source); err != nil {
		return err
	}
	if err := unchanged(ctx, dst, a.Path, a.Destination); err != nil {
		return err
	}
	sp, dp := src.At(a.Path), dst.At(a.Path)
	parent := path.Dir(dp)
	if _, ok := dst.FS.(*Local); ok {
		parent = filepath.Dir(dp)
	}
	if err := dst.FS.Mkdir(ctx, parent); err != nil {
		return err
	}
	sr, sremote := src.FS.(*Remote)
	dr, dremote := dst.FS.(*Remote)
	if sremote && dremote && sr.UID == dr.UID && a.Destination == nil {
		if err := sr.copyFile(sp, dp); err != nil {
			return err
		}
	} else if !dremote {
		if err := safeParents(dp); err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(dp), ".pcs-rc-*")
		if err != nil {
			return err
		}
		tmp := f.Name()
		f.Close()
		defer os.Remove(tmp)
		if sremote {
			err = runTransferWorker(ctx, "download", sr.UID, sp, tmp, perTransferRate(o))
		} else {
			err = copyLocal(ctx, sp, tmp, perTransferRate(o))
		}
		if err != nil {
			return err
		}
		st, err := os.Stat(tmp)
		if err != nil {
			return err
		}
		if st.Size() != a.Source.Size {
			return errors.New("downloaded size mismatch")
		}
		if err = unchanged(ctx, src, a.Path, a.Source); err != nil {
			return err
		}
		if err = unchanged(ctx, dst, a.Path, a.Destination); err != nil {
			return err
		}
		if err = os.Chtimes(tmp, a.Source.ModTime, a.Source.ModTime); err != nil {
			return err
		}
		if err = os.Rename(tmp, dp); err != nil {
			return err
		}
	} else {
		local := sp
		if sremote {
			temp, err := os.MkdirTemp("", "pcs-rc-relay-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(temp)
			local = filepath.Join(temp, "content")
			if err = runTransferWorker(ctx, "download", sr.UID, sp, local, perTransferRate(o)); err != nil {
				return err
			}
		}
		if err := runTransferWorker(ctx, "upload", dr.UID, local, dp, perTransferRate(o)); err != nil {
			return err
		}
	}
	if err := unchanged(ctx, src, a.Path, a.Source); err != nil {
		return err
	}
	got, err := dst.FS.Stat(ctx, dp)
	if err != nil {
		return err
	}
	if got.IsDir || got.Size != a.Source.Size {
		return errors.New("post-transfer size mismatch")
	}
	if o.Checksum {
		x, err := src.FS.Hash(ctx, sp)
		if err != nil {
			return err
		}
		y, err := dst.FS.Hash(ctx, dp)
		if err != nil {
			return err
		}
		if x != y {
			return errors.New("post-transfer checksum mismatch")
		}
	}
	return nil
}
func perTransferRate(o Options) int64 {
	if o.BWLimit <= 0 {
		return 0
	}
	n := o.Transfers
	if n < 1 {
		n = 1
	}
	v := o.BWLimit / int64(n)
	if v < 1 {
		return 1
	}
	return v
}
func copyLocal(ctx context.Context, src, dst string, rate int64) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 128*1024)
	start := time.Now()
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, re := f.Read(buf)
		if n > 0 {
			if _, err = out.Write(buf[:n]); err != nil {
				return err
			}
			total += int64(n)
			if rate > 0 {
				wait := time.Duration(float64(total)/float64(rate)*float64(time.Second)) - time.Since(start)
				if wait > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(wait):
					}
				}
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return re
		}
	}
	return out.Sync()
}
func removeEmpty(ctx context.Context, e Endpoint, includeRoot bool, o Options) error {
	m, err := snapshot(ctx, e)
	if err != nil {
		return err
	}
	keys := sortedKeys(m)
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	if includeRoot {
		keys = append(keys, "")
	}
	for _, k := range keys {
		if k != "" && !m[k].IsDir {
			continue
		}
		empty := true
		prefix := k + "/"
		if k == "" {
			prefix = ""
		}
		for child := range m {
			if child != k && strings.HasPrefix(child, prefix) {
				empty = false
				break
			}
		}
		if !empty {
			continue
		}
		if o.DryRun {
			logAction(o, "would-rmdir", k)
		} else {
			if err = e.FS.Remove(ctx, e.At(k), true); err != nil {
				return err
			}
			logAction(o, "rmdir", k)
		}
		delete(m, k)
	}
	return nil
}

func tryServerMove(ctx context.Context, src, dst Endpoint, a Action, o Options) (bool, error) {
	sr, ok := src.FS.(*Remote)
	if !ok {
		return false, nil
	}
	dr, ok := dst.FS.(*Remote)
	if !ok || sr.UID != dr.UID || a.Destination != nil {
		return false, nil
	}
	if err := unchanged(ctx, src, a.Path, a.Source); err != nil {
		return true, err
	}
	if err := unchanged(ctx, dst, a.Path, nil); err != nil {
		return true, err
	}
	sp, dp := src.At(a.Path), dst.At(a.Path)
	if err := dr.Mkdir(ctx, path.Dir(dp)); err != nil {
		return true, err
	}
	if err := sr.moveFile(sp, dp); err != nil {
		return true, err
	}
	got, err := dr.Stat(ctx, dp)
	if err != nil {
		return true, err
	}
	if got.Size != a.Source.Size || got.IsDir || (a.Source.ID != "" && got.ID != a.Source.ID) {
		return true, errors.New("server move result could not be verified")
	}
	if _, err = sr.Stat(ctx, sp); !errors.Is(err, os.ErrNotExist) {
		return true, errors.New("server move source removal could not be verified")
	}
	return true, nil
}
