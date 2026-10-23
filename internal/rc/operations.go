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
	if _, ok := dst.FS.(*Local); ok {
		if err = validLocalName(filepath.Base(dst.Root)); err != nil {
			return nil, err
		}
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

func Check(ctx context.Context, src, dst Endpoint, o Options) error {
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
		s := sm[k]
		if s.IsDir || !o.Filter.Match(k) {
			continue
		}
		d, ok := dm[k]
		if !ok {
			logAction(o, "missing-on-destination", k)
			differences++
			continue
		}
		if d.IsDir {
			logAction(o, "different-type", k)
			differences++
			continue
		}
		equal, err := same(ctx, src, dst, k, s, d, o)
		if err != nil {
			return err
		}
		if !equal {
			logAction(o, "different", k)
			differences++
		} else {
			logAction(o, "match", k)
		}
	}
	for _, k := range sortedKeys(dm) {
		d := dm[k]
		if d.IsDir || !o.Filter.Match(k) {
			continue
		}
		if s, ok := sm[k]; ok && s.IsDir {
			logAction(o, "different-type", k)
			differences++
		} else if !ok {
			logAction(o, "missing-on-source", k)
			differences++
		}
	}
	if differences > 0 {
		return fmt.Errorf("%d differences found", differences)
	}
	return nil
}
func Delete(ctx context.Context, e Endpoint, mode string, o Options) error {
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
			logAction(o, "would-delete", e.Root)
			return nil
		}
		return e.FS.Remove(ctx, e.Root, false)
	}
	if mode == "rmdir" {
		children, err := e.FS.List(ctx, e.Root)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			return errors.New("directory is not empty")
		}
		if o.DryRun {
			logAction(o, "would-rmdir", e.Root)
			return nil
		}
		return e.FS.Remove(ctx, e.Root, true)
	}
	if mode == "rmdirs" {
		return removeEmpty(ctx, e, false, o)
	}
	if mode == "purge" && (e.Root == "/" || filepath.Dir(e.Root) == e.Root) {
		return errors.New("refusing to purge a filesystem root")
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
	for _, k := range files {
		if o.DryRun {
			logAction(o, "would-delete", k)
			continue
		}
		if err = e.FS.Remove(ctx, e.At(k), false); err != nil {
			return err
		}
		logAction(o, "delete", k)
	}
	if mode == "purge" {
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		keys = append(keys, "")
		for _, k := range keys {
			if k != "" && !m[k].IsDir {
				continue
			}
			if o.DryRun {
				logAction(o, "would-rmdir", k)
				continue
			}
			if err = e.FS.Remove(ctx, e.At(k), true); err != nil {
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
		v := m[k]
		if (v.IsDir && !f.excluded(k)) || (!v.IsDir && f.Match(k)) {
			out = append(out, v)
		}
	}
	return out, nil
}
func snapshotDepth(ctx context.Context, e Endpoint, recursive bool, depth int) (map[string]Entry, error) {
	out := map[string]Entry{}
	var walk func(string, int) error
	walk = func(rel string, level int) error {
		items, err := e.FS.List(ctx, e.At(rel))
		if err != nil {
			return err
		}
		for _, v := range items {
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
		return nil
	}
	err := walk("", 1)
	return out, err
}
