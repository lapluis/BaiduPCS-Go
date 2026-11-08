package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ValidateMkdirTarget rejects files occupying a directory target or an ancestor
// of that target, without creating any directories.
func ValidateMkdirTarget(ctx context.Context, e Endpoint) (validationErr error) {
	defer func() { validationErr = preserveContextError(ctx, validationErr) }()
	for current := e.Root; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := e.FS.Stat(ctx, current)
		if err == nil {
			if !entry.IsDir {
				return fmt.Errorf("file occupies directory: %s", current)
			}
			return ctx.Err()
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := path.Dir(current)
		if _, local := e.FS.(*Local); local {
			parent = filepath.Dir(current)
		}
		if parent == current {
			return err
		}
		current = parent
	}
}

func FilePlan(ctx context.Context, src, dst Endpoint, move bool, o Options) (*Plan, error) {
	if err := validatePair(src, dst); err != nil {
		return nil, err
	}
	s, err := src.FS.Stat(ctx, src.Root)
	if err != nil {
		return nil, err
	}
	if s.IsDir {
		return nil, errors.New("copyto/moveto expects a single file")
	}
	a := Action{Kind: "copy", Source: &s}
	d, err := dst.FS.Stat(ctx, dst.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if d.IsDir {
			return nil, errors.New("target is a directory; specify a filename")
		}
		a.Destination = &d
		if o.IgnoreExisting {
			a.Kind = "skip"
			a.Reason = "ignore-existing"
		} else if o.Update && d.ModTime.After(s.ModTime) {
			a.Kind = "skip"
			a.Reason = "destination-newer"
		} else {
			equal, err := same(ctx, src, dst, "", s, d, o)
			if err != nil {
				return nil, err
			}
			if equal {
				a.Kind = "skip"
				a.Reason = "identical"
			}
		}
	}
	return &Plan{Source: src, Destination: dst, Actions: []Action{a}, Move: move}, nil
}

func Check(ctx context.Context, src, dst Endpoint, o Options) (checkErr error) {
	defer func() { checkErr = preserveContextError(ctx, checkErr) }()
	// An explicit check compares current content even when a prior download
	// has an unchanged entity validator and a verified local completion record.
	if !o.SizeOnly {
		o.Checksum = true
	}
	sm, err := snapshot(ctx, src)
	if err != nil {
		return err
	}
	dm, err := snapshot(ctx, dst)
	if err != nil {
		return err
	}
	differences := 0
	for _, k := range sortedKeys(sm) {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := sm[k]
		if s.IsDir || !o.Filter.Match(k) {
			continue
		}
		d, ok := dm[k]
		if !ok {
			if err := logAction(o, "missing-on-destination", k); err != nil {
				return err
			}
			differences++
			continue
		}
		if d.IsDir {
			if err := logAction(o, "different-type", k); err != nil {
				return err
			}
			differences++
			continue
		}
		equal, err := same(ctx, src, dst, k, s, d, o)
		if err != nil {
			return err
		}
		if !equal {
			if err := logAction(o, "different", k); err != nil {
				return err
			}
			differences++
		} else {
			if err := logAction(o, "match", k); err != nil {
				return err
			}
		}
	}
	for _, k := range sortedKeys(dm) {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := dm[k]
		if d.IsDir || !o.Filter.Match(k) {
			continue
		}
		if s, ok := sm[k]; ok && s.IsDir {
			if err := logAction(o, "different-type", k); err != nil {
				return err
			}
			differences++
		} else if !ok {
			if err := logAction(o, "missing-on-source", k); err != nil {
				return err
			}
			differences++
		}
	}
	if differences > 0 {
		return fmt.Errorf("%d differences found", differences)
	}
	return nil
}
func Delete(ctx context.Context, e Endpoint, mode string, o Options) (deleteErr error) {
	defer func() { deleteErr = preserveContextError(ctx, deleteErr) }()
	if mode == "deletefile" {
		item, err := e.FS.Stat(ctx, e.Root)
		if err != nil {
			return err
		}
		if item.IsDir {
			return errors.New("deletefile expects a file")
		}
		if o.MaxDelete == 0 {
			return errors.New("--max-delete is zero")
		}
		if o.DryRun {
			return logAction(o, "would-delete", e.Root)
		}
		if err := unchanged(ctx, e, "", &item); err != nil {
			return err
		}
		if err := e.FS.Remove(ctx, e.Root, false); err != nil {
			return err
		}
		return logAction(o, "delete", e.Root)
	}
	if mode == "rmdir" {
		item, err := e.FS.Stat(ctx, e.Root)
		if err != nil {
			return err
		}
		if !item.IsDir {
			return errors.New("rmdir expects a directory")
		}
		empty, err := directoryEmptyAfterRemovals(ctx, e, "", nil)
		if err != nil {
			return err
		}
		if !empty {
			return errors.New("directory is not empty")
		}
		if err := unchangedDirectory(ctx, e, "", &item); err != nil {
			return err
		}
		if o.DryRun {
			return logAction(o, "would-rmdir", e.Root)
		}
		if err := e.FS.Remove(ctx, e.Root, true); err != nil {
			return err
		}
		return logAction(o, "rmdir", e.Root)
	}
	if mode == "rmdirs" {
		return removeEmpty(ctx, e, false, o)
	}
	if mode == "purge" && (e.Root == "/" || localDir(e.Root) == e.Root) {
		return errors.New("refusing to purge a filesystem root")
	}
	var purgeRoot Entry
	if mode == "purge" {
		var err error
		purgeRoot, err = e.FS.Stat(ctx, e.Root)
		if err != nil {
			return err
		}
	}
	m, err := snapshot(ctx, e)
	if err != nil {
		return err
	}
	keys := sortedKeys(m)
	var files []string
	for _, k := range keys {
		if !m[k].IsDir && (mode == "purge" || o.Filter.Match(k)) {
			files = append(files, k)
		}
	}
	if o.MaxDelete >= 0 && len(files) > o.MaxDelete {
		return fmt.Errorf("planned deletions %d exceed --max-delete %d", len(files), o.MaxDelete)
	}
	for _, k := range files {
		old := m[k]
		if err = unchanged(ctx, e, k, &old); err != nil {
			return err
		}
	}
	removed := make(map[string]bool)
	for _, k := range files {
		if o.DryRun {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := logAction(o, "would-delete", k); err != nil {
				return err
			}
			removed[k] = true
			continue
		}
		old := m[k]
		if err = unchanged(ctx, e, k, &old); err != nil {
			return err
		}
		if err = e.FS.Remove(ctx, e.At(k), false); err != nil {
			return err
		}
		if err := logAction(o, "delete", k); err != nil {
			return err
		}
	}
	if mode == "purge" {
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		keys = append(keys, "")
		for _, k := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			if k != "" && !m[k].IsDir {
				continue
			}
			item := k
			if item == "" {
				item = e.Root
			}
			old := m[k]
			if k == "" {
				old = purgeRoot
			}
			if err = unchangedDirectory(ctx, e, k, &old); err != nil {
				return err
			}
			if o.DryRun {
				empty, err := directoryEmptyAfterRemovals(ctx, e, k, removed)
				if err != nil {
					return err
				}
				if !empty {
					return fmt.Errorf("directory is not empty: %s", item)
				}
				if err := unchangedDirectory(ctx, e, k, &old); err != nil {
					return err
				}
				if err := logAction(o, "would-rmdir", item); err != nil {
					return err
				}
				removed[k] = true
				continue
			}
			if err = e.FS.Remove(ctx, e.At(k), true); err != nil {
				return err
			}
			if err := logAction(o, "rmdir", item); err != nil {
				return err
			}
		}
	}
	return nil
}
func Listing(ctx context.Context, e Endpoint, recursive bool, depth int, f Filter) ([]Entry, error) {
	m, err := snapshotDepth(ctx, e, recursive, depth)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(m))
	for _, k := range sortedKeys(m) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v := m[k]
		if (v.IsDir && !f.excluded(k)) || (!v.IsDir && f.Match(k)) {
			out = append(out, v)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func snapshotDepth(ctx context.Context, e Endpoint, recursive bool, depth int) (map[string]Entry, error) {
	out := map[string]Entry{}
	var walk func(string, int) error
	walk = func(rel string, level int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := e.FS.List(ctx, e.At(rel))
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, v := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if v.Name == "." || v.Name == ".." || strings.ContainsAny(v.Name, "/\\") {
				return errors.New("unsafe directory entry")
			}
			v.Path = path.Join(rel, v.Name)
			out[v.Path] = v
			if v.IsDir && recursive && (depth < 0 || level < depth) {
				if err = walk(v.Path, level+1); err != nil {
					return err
				}
			}
		}
		return ctx.Err()
	}
	err := walk("", 1)
	return out, err
}
