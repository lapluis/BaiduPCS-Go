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
	DownloadThreads, MaxConnections                    int
	BWLimit                                            int64
	Filter                                             Filter
	Emit                                               func(string, string)
	Progress                                           func(ProgressSnapshot)
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
	Canceled, Pending                int
	Bytes                            int64
}

type comparisonParallelKey struct{}

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
	if err := ValidateDestination(b); err != nil {
		return err
	}
	if a.FS.Identity() != b.FS.Identity() {
		return nil
	}
	x, y := strings.TrimRight(filepath.ToSlash(a.Root), "/"), strings.TrimRight(filepath.ToSlash(b.Root), "/")
	overlap := x == y || strings.HasPrefix(x, y+"/") || strings.HasPrefix(y, x+"/")
	if a.FS.Identity() == "local" {
		var err error
		overlap, err = localPathsOverlap(x, y)
		if err != nil {
			return err
		}
	}
	if overlap {
		return errors.New("source and destination overlap")
	}
	if _, local := a.FS.(*Local); local {
		if _, local = b.FS.(*Local); local {
			return validateLocalOverlap(a.Root, b.Root)
		}
	}
	return nil
}

// Lexical comparisons miss Windows path aliases and hard links. Compare each
// existing root with the other path and its ancestors, including when a new
// destination is nested beneath an alias of the source directory.
func validateLocalOverlap(a, b string) error {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		root, err := os.Stat(pair[0])
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for p := pair[1]; ; p = filepath.Dir(p) {
			info, err := os.Stat(p)
			if err == nil && os.SameFile(root, info) {
				return errors.New("source and destination overlap (same filesystem object)")
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	return nil
}

// ValidateDestination checks local target names before previewing or executing
// a write. Remote paths retain their backend's naming rules.
func ValidateDestination(e Endpoint) error {
	if _, local := e.FS.(*Local); local {
		return validLocalPath(e.Root)
	}
	return nil
}

// Validate components after the volume separately: drive and UNC prefixes are
// path syntax, while every directory and filename must be representable.
func validLocalPath(p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	p = filepath.Clean(p)
	p = strings.TrimLeft(p[len(filepath.VolumeName(p)):], `/\`)
	if p == "" || p == "." {
		return nil
	}
	return validLocalName(filepath.ToSlash(p))
}

func validLocalName(p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	for _, n := range strings.Split(p, "/") {
		if strings.ContainsAny(n, `<>:"\|?*`) || strings.TrimRight(n, ". ") != n || strings.ContainsFunc(n, func(r rune) bool { return r < 32 }) {
			return fmt.Errorf("unsupported Windows filename %q", n)
		}
		// Device aliases also apply before an extension and after trimming base
		// spaces. Let the Windows path library cover console names and superscript
		// port digits; an empty base is valid for ordinary dotfiles.
		base := strings.TrimRight(strings.SplitN(n, ".", 2)[0], " ")
		if base != "" && !filepath.IsLocal(base) {
			return fmt.Errorf("reserved Windows filename %q", n)
		}
	}
	return nil
}
func same(ctx context.Context, src, dst Endpoint, p string, a, b Entry, o Options) (equal bool, compareErr error) {
	defer func() {
		if compareErr == nil {
			compareErr = unchanged(ctx, src, p, &a)
		}
		if compareErr == nil {
			compareErr = unchanged(ctx, dst, p, &b)
		}
		if compareErr != nil {
			equal = false
		}
	}()
	if a.Size != b.Size {
		return false, nil
	}
	if o.SizeOnly {
		return true, nil
	}
	if !o.Checksum {
		if sr, ok := src.FS.(*Remote); ok {
			if _, local := dst.FS.(*Local); local {
				if verified, err := verifiedDownloadedFile(ctx, sr, src.At(p), dst.At(p), a, b); err != nil || verified {
					return verified, err
				}
			}
		}
	}
	parallel, _ := ctx.Value(comparisonParallelKey{}).(int)
	ctx = hashContext(ctx, o, parallel)
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
	if _, local := dst.FS.(*Local); local {
		if err := validateSelectedLocalPaths(sm, dm, o); err != nil {
			return nil, err
		}
	}
	var candidates []Action
	for _, k := range sortedKeys(sm) {
		s := sm[k]
		d, exists := dm[k]
		if s.IsDir {
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
	comparisons := 0
	for _, a := range candidates {
		if a.Kind == "copy" && a.Destination != nil {
			comparisons++
		}
	}
	budget := o.MaxConnections
	if budget < 1 {
		budget = 16
	}
	n := min(max(1, o.Checkers), budget, max(1, comparisons))
	// Every active content worker needs at least one byte/s of integer quota.
	if !o.SizeOnly && o.BWLimit > 0 && o.BWLimit < int64(n) {
		n = int(o.BWLimit)
	}
	compareCtx := context.WithValue(ctx, comparisonParallelKey{}, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var compareErr error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					continue
				}
				a := &candidates[i]
				if a.Kind != "copy" || a.Destination == nil {
					continue
				}
				equal, e := same(compareCtx, src, dst, a.Path, *a.Source, *a.Destination, o)
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
dispatchComparisons:
	for i := range candidates {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatchComparisons
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, errors.Join(compareErr, ctx.Err())
	}
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
	if current.Size != old.Size || current.IsDir != old.IsDir || !current.ModTime.Equal(old.ModTime) || !sameIdentity(current, *old) {
		return fmt.Errorf("file changed after planning: %s", p)
	}
	return nil
}

// Removing planned children legitimately changes a directory's size and mtime.
// Its identity must still match before removing the directory itself.
func unchangedDirectory(ctx context.Context, e Endpoint, p string, old *Entry) error {
	if old == nil || !old.IsDir {
		return fmt.Errorf("missing planned directory identity: %s", p)
	}
	current, err := e.FS.Stat(ctx, e.At(p))
	if err != nil {
		return err
	}
	if !current.IsDir || !sameIdentity(current, *old) {
		return fmt.Errorf("directory changed after planning: %s", p)
	}
	return nil
}
func actionPath(p *Plan, a Action) string {
	if a.Path != "" {
		return a.Path
	}
	return p.Source.Root + " -> " + p.Destination.Root
}

// ErrLogCallback identifies a failed action-log observer.
var ErrLogCallback = errors.New("action log callback failed")

func logAction(o Options, kind, p string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			cause, ok := r.(error)
			if !ok {
				cause = fmt.Errorf("%v", r)
			}
			err = fmt.Errorf("%w (%s): %w", ErrLogCallback, kind, cause)
		}
	}()
	if o.Emit != nil {
		o.Emit(kind, p)
	}
	return nil
}
func preserveContextError(ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil && !errors.Is(err, canceled) {
		return errors.Join(err, canceled)
	}
	return err
}

func Execute(ctx context.Context, p *Plan, o Options) (out Result, executeErr error) {
	defer func(parent context.Context) { executeErr = preserveContextError(parent, executeErr) }(ctx)
	result := Result{}
	if o.DryRun {
		var removedFiles []string
		removedDestination := make(map[string]bool)
		for _, a := range p.Actions {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if a.Kind == "rmdir" {
				if err := unchangedDirectory(ctx, p.Destination, a.Path, a.Destination); err != nil {
					return result, err
				}
				empty, err := directoryEmptyAfterRemovals(ctx, p.Destination, a.Path, removedDestination)
				if err != nil {
					return result, err
				}
				if !empty {
					continue
				}
				if err := unchangedDirectory(ctx, p.Destination, a.Path, a.Destination); err != nil {
					return result, err
				}
			}
			if err := logAction(o, "would-"+a.Kind, actionPath(p, a)); err != nil {
				return result, err
			}
			if a.Kind == "delete" || a.Kind == "rmdir" {
				removedDestination[a.Path] = true
			}
			if p.Move && (a.Kind == "copy" || a.Reason == "identical") {
				if err := ctx.Err(); err != nil {
					return result, err
				}
				if err := logAction(o, "would-remove-source", actionPath(p, a)); err != nil {
					return result, err
				}
				removedFiles = append(removedFiles, a.Path)
			}
		}
		if p.Move && o.DeleteEmptyDirs && p.SourceSnapshot != nil {
			return result, removeEmptyPlanned(ctx, p.Source, false, o, p.SourceSnapshot, removedFiles...)
		}
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		for _, a := range p.Actions {
			if a.Kind == "copy" || a.Kind == "skip" {
				result.Pending++
			}
		}
		return result, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	for _, a := range p.Actions {
		if a.Kind == "mkdir" {
			if err := p.Destination.FS.Mkdir(ctx, p.Destination.At(a.Path)); err != nil {
				return result, err
			}
		}
	}
	progress := newProgress(p)
	budget := o.MaxConnections
	if budget < 1 {
		budget = 16
	}
	n := min(max(1, o.Transfers), budget, max(1, progress.totalFiles))
	contentJobs := 0
	for _, a := range p.Actions {
		if a.Kind == "copy" || p.Move && a.Kind == "skip" && a.Reason == "identical" {
			contentJobs++
		}
	}
	if o.BWLimit > 0 && o.BWLimit < int64(min(n, contentJobs)) {
		n = int(o.BWLimit)
	}
	// Metadata-only skips do not consume download or bandwidth shares.
	o.Transfers = min(n, max(1, contentJobs))
	jobs := make(chan Action)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs error
	active := 0
	if o.ShowProgress {
		done := make(chan struct{})
		joined := make(chan struct{})
		var progressErr error // Published when the progress goroutine closes joined.
		defer func() {
			close(done)
			<-joined
			if progressErr != nil && !errors.Is(executeErr, progressErr) {
				executeErr = errors.Join(executeErr, progressErr)
			}
		}()
		go func() {
			defer close(joined)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					mu.Lock()
					snapshot := progress.snapshot(result, active)
					mu.Unlock()
					snapshot.Final = true
					progressErr = runProgressCallback(o, snapshot)
					return
				case <-ticker.C:
					mu.Lock()
					snapshot := progress.snapshot(result, active)
					mu.Unlock()
					if progressErr = runProgressCallback(o, snapshot); progressErr != nil {
						cancel(progressErr)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				if ctx.Err() != nil {
					continue
				}
				mu.Lock()
				active++
				progress.start(a)
				mu.Unlock()
				actionCtx := context.WithValue(ctx, transferSettingsKey{}, workerSettings{Threads: downloadThreads(o)})
				actionCtx = context.WithValue(actionCtx, progressKey{}, func(v transferProgress) { mu.Lock(); progress.update(a.Path, v); mu.Unlock() })
				var err error
				if a.Kind == "skip" {
					err = unchanged(ctx, p.Source, a.Path, a.Source)
					if err == nil {
						err = unchanged(ctx, p.Destination, a.Path, a.Destination)
					}
					if err == nil && p.Move && a.Reason == "identical" {
						err = verifyAndRemove(ctx, p.Source, p.Destination, a, o)
					}
					if err == nil {
						mu.Lock()
						result.Skipped++
						mu.Unlock()
					}
				} else {
					serverMoved := false
					if p.Move {
						serverMoved, err = tryServerMove(ctx, p.Source, p.Destination, a, o)
					}
					for attempt := 0; !serverMoved && attempt <= o.Retries; attempt++ {
						err = copyAction(actionCtx, p.Source, p.Destination, &a, o)
						// Restarting a worker must not hide failures saving state or
						// observing its output, even if a later attempt could succeed.
						if err == nil || ctx.Err() != nil || errors.Is(err, ErrCheckpointSave) || errors.Is(err, ErrWorkerProtocol) || errors.Is(err, ErrProgressCallback) || errors.Is(err, ErrLogCallback) {
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
				if cause := context.Cause(ctx); err != nil && (errors.Is(cause, ErrProgressCallback) || errors.Is(cause, ErrLogCallback)) {
					err = errors.Join(err, cause)
				}
				mu.Lock()
				active--
				progress.finish(a, err == nil)
				// A request's own timeout is a failure while the parent task is live.
				canceled := errors.Is(err, context.Canceled) || ctx.Err() != nil && errors.Is(err, context.DeadlineExceeded)
				var logErr error
				if canceled && !errors.Is(err, ErrCheckpointSave) && !errors.Is(err, ErrWorkerProtocol) && !errors.Is(err, ErrProgressCallback) && !errors.Is(err, ErrLogCallback) {
					result.Canceled++
					errs = errors.Join(errs, err)
				} else if err != nil {
					result.Failed++
					errs = errors.Join(errs, fmt.Errorf("%s: %w", a.Path, err))
					logErr = logAction(o, "error", actionPath(p, a))
				} else {
					logErr = logAction(o, a.Kind, actionPath(p, a))
				}
				if logErr != nil {
					errs = errors.Join(errs, logErr)
					cancel(logErr)
				}
				mu.Unlock()
			}
		}()
	}
dispatch:
	for _, a := range p.Actions {
		if a.Kind == "copy" || a.Kind == "skip" {
			if ctx.Err() != nil {
				break
			}
			select {
			case jobs <- a:
			case <-ctx.Done():
				break dispatch
			}
		}
	}
	close(jobs)
	wg.Wait()
	mu.Lock()
	result.Pending = progress.totalFiles - result.Copied - result.Skipped - result.Failed - result.Canceled
	mu.Unlock()
	if errs != nil {
		return result, errors.Join(errs, ctx.Err())
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
			if !ok || cur.IsDir != old.IsDir || !sameIdentity(cur, old) || (!cur.IsDir && (cur.Size != old.Size || !cur.ModTime.Equal(old.ModTime))) {
				return result, errors.New("source tree changed; deletion blocked")
			}
		}
	}
	// All deletion candidates are rechecked before the first delete.
	for _, a := range p.Actions {
		if a.Kind == "rmdir" {
			if err := unchangedDirectory(ctx, p.Destination, a.Path, a.Destination); err != nil {
				return result, err
			}
		}
		if a.Kind == "delete" {
			if err := unchanged(ctx, p.Destination, a.Path, a.Destination); err != nil {
				return result, err
			}
		}
	}
	for _, a := range p.Actions {
		if a.Kind == "delete" {
			// Earlier deletions may take long enough for later candidates to change.
			if err := unchanged(ctx, p.Destination, a.Path, a.Destination); err != nil {
				return result, err
			}
			if err := p.Destination.FS.Remove(ctx, p.Destination.At(a.Path), false); err != nil {
				return result, err
			}
			mu.Lock()
			result.Deleted++
			mu.Unlock()
			if err := logAction(o, "delete", a.Path); err != nil {
				return result, err
			}
		}
	}
	for _, a := range p.Actions {
		if a.Kind == "rmdir" {
			if err := unchangedDirectory(ctx, p.Destination, a.Path, a.Destination); err != nil {
				return result, err
			}
			empty, err := directoryEmptyAfterRemovals(ctx, p.Destination, a.Path, nil)
			if err != nil {
				return result, err
			}
			if empty {
				if err := unchangedDirectory(ctx, p.Destination, a.Path, a.Destination); err != nil {
					return result, err
				}
				if err = p.Destination.FS.Remove(ctx, p.Destination.At(a.Path), true); err != nil {
					return result, err
				}
				if err := logAction(o, "rmdir", a.Path); err != nil {
					return result, err
				}
			}
		}
	}
	if p.Move && o.DeleteEmptyDirs && p.SourceSnapshot != nil {
		if err := removeEmptyPlanned(ctx, p.Source, false, o, p.SourceSnapshot); err != nil {
			return result, err
		}
	}
	return result, nil
}
func verifyAndRemove(ctx context.Context, src, dst Endpoint, a Action, o Options) error {
	ctx = hashContext(ctx, o, o.Transfers)
	if err := unchanged(ctx, src, a.Path, a.Source); err != nil {
		return err
	}
	destination, err := dst.FS.Stat(ctx, dst.At(a.Path))
	if err != nil {
		return err
	}
	if destination.IsDir || destination.Size != a.Source.Size {
		return errors.New("destination changed before verification; source retained")
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
	// Recheck immediately before removal: the destination may have become an
	// alias after planning. Equal hashes alone do not prove a second copy exists.
	if _, local := src.FS.(*Local); local {
		if _, local = dst.FS.(*Local); local {
			sourceInfo, err := os.Stat(src.At(a.Path))
			if err != nil {
				return err
			}
			destinationInfo, err := os.Stat(dst.At(a.Path))
			if err != nil {
				return err
			}
			if os.SameFile(sourceInfo, destinationInfo) {
				return errors.New("source and destination refer to the same file; source retained")
			}
		}
	}
	if err := unchanged(ctx, dst, a.Path, &destination); err != nil {
		return fmt.Errorf("destination changed during verification; source retained: %w", err)
	}
	return src.FS.Remove(ctx, src.At(a.Path), false)
}
func copyAction(ctx context.Context, src, dst Endpoint, a *Action, o Options) error {
	var completedStage *downloadStage
	var completedDigest string
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
		if err := sr.copyFile(ctx, sp, dp); err != nil {
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
		if err := f.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		var stage *downloadStage
		if sremote {
			_ = os.Remove(tmp)
			base, _, e := downloadBase(sr.UID, sp, dp)
			if e != nil {
				return e
			}
			if e = prepareStageDirectory(filepath.Dir(base)); e != nil {
				return e
			}
			lease, e := lockDownload(base + ".lock")
			if e != nil {
				return fmt.Errorf("download is already in use or state cannot be locked: %w", e)
			}
			defer lease.Close()
			prepared, e := stageFor(ctx, sr, sp, dp, *a.Source)
			if e != nil {
				return e
			}
			stage = &prepared
			tmp = stage.payload
			settings, _ := ctx.Value(transferSettingsKey{}).(workerSettings)
			settings.ETag = stage.info.ETag
			settings.Modified = stage.info.LastModified
			transferCtx := context.WithValue(ctx, transferSettingsKey{}, settings)
			transferCtx = context.WithValue(transferCtx, progressKey{}, func(v transferProgress) {
				if v.ETag != "" {
					stage.info.ETag = v.ETag
				}
				if v.Modified != "" {
					stage.info.LastModified = v.Modified
				}
				reportProgress(ctx, v)
			})
			err = runTransferWorker(transferCtx, "download", sr.UID, sp, tmp, perTransferRate(o))
			if errors.Is(err, errResumeIdentity) {
				return errors.Join(err, stage.reset())
			}
			if saveErr := stage.save(); saveErr != nil {
				return errors.Join(err, saveErr)
			}
		} else {
			defer os.Remove(tmp)
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
		if stage != nil && stage.canRecordCompletion() {
			// Bind the completion record to the downloaded payload before the
			// destination becomes visible to other programs. Hashing the published
			// file later could accidentally certify a concurrent editor's changes.
			completedDigest, err = hashSHA256(ctx, tmp)
			if err != nil {
				return err
			}
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
		if stage != nil {
			stage.cleanup()
			completedStage = stage
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
	// A successful write replaces the planned destination. Keep its snapshot
	// for retries after verification errors, without accepting later changes.
	// If the write or stat failed, retain the old snapshot and fail closed.
	a.Destination = &got
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
	if err := unchanged(ctx, src, a.Path, a.Source); err != nil {
		return err
	}
	if err := unchanged(ctx, dst, a.Path, a.Destination); err != nil {
		return err
	}
	if completedStage != nil {
		_ = completedStage.complete(ctx, dp, completedDigest)
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

func hashContext(ctx context.Context, o Options, parallel int) context.Context {
	o.Transfers = max(1, parallel)
	ctx = context.WithValue(ctx, hashRateKey{}, perTransferRate(o))
	return context.WithValue(ctx, transferSettingsKey{}, workerSettings{Threads: downloadThreads(o)})
}

type localCopyOutput interface {
	io.Writer
	Sync() error
	Close() error
}

var openLocalCopyOutput = func(path string) (localCopyOutput, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
}

func copyLocal(ctx context.Context, src, dst string, rate int64) (resultErr error) {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := openLocalCopyOutput(dst)
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close local copy: %w", err))
		}
	}()
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
			reportProgress(ctx, transferProgress{Done: total, Total: -1, Rate: int64(float64(total) / time.Since(start).Seconds())})
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
	return removeEmptyPlanned(ctx, e, includeRoot, o, nil)
}

func removeEmptyPlanned(ctx context.Context, e Endpoint, includeRoot bool, o Options, planned map[string]Entry, removedFiles ...string) (removeErr error) {
	defer func() { removeErr = preserveContextError(ctx, removeErr) }()
	var root Entry
	if includeRoot {
		var err error
		root, err = e.FS.Stat(ctx, e.Root)
		if err != nil {
			return err
		}
	}
	m, err := snapshot(ctx, e)
	if err != nil {
		return err
	}
	removed := make(map[string]bool)
	if o.DryRun {
		// A move preview has not removed its files. Simulate only the source
		// removals already reported so cleanup sees the predicted empty tree.
		for _, name := range removedFiles {
			if item, ok := m[name]; ok && !item.IsDir {
				delete(m, name)
				removed[name] = true
			}
		}
	}
	keys := sortedKeys(m)
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	if includeRoot {
		keys = append(keys, "")
	}
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if k != "" && !m[k].IsDir {
			continue
		}
		if k != "" && o.Filter.excluded(k) {
			continue
		}
		old := m[k]
		if k == "" {
			old = root
		}
		if planned != nil && k != "" {
			var ok bool
			old, ok = planned[k]
			if !ok || !old.IsDir {
				continue
			}
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
		empty, err = directoryEmptyAfterRemovals(ctx, e, k, removed)
		if err != nil {
			return err
		}
		if !empty {
			continue
		}
		if err = unchangedDirectory(ctx, e, k, &old); err != nil {
			return err
		}
		if o.DryRun {
			if err := logAction(o, "would-rmdir", k); err != nil {
				return err
			}
		} else {
			if err = e.FS.Remove(ctx, e.At(k), true); err != nil {
				return err
			}
			if err := logAction(o, "rmdir", k); err != nil {
				return err
			}
		}
		delete(m, k)
		if o.DryRun {
			removed[k] = true
		}
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
	if err := sr.moveFile(ctx, sp, dp); err != nil {
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
		if err != nil {
			return true, fmt.Errorf("server move source removal could not be verified: %w", err)
		}
		return true, errors.New("server move source removal could not be verified")
	}
	return true, nil
}
