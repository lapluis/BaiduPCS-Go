package rc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

const Help = `Rclone-style commands (legacy commands remain available):
  copy SOURCE DEST             Copy directory contents; retain extra destination files
  copyto FILE TARGET           Copy one file to an exact target filename
  sync SOURCE DEST             Mirror source files to destination; delete extras
  move SOURCE DEST             Copy and verify files before removing source files
  moveto FILE TARGET           Move one file to an exact target filename
  check SOURCE DEST            Compare directory contents without changing files
  lsf|lsjson|lsd PATH           List entries (use --recursive for descendants)
  size PATH                    Count files and bytes recursively
  delete PATH                  Delete matching files, keep directories
  deletefile FILE              Delete exactly one file
  purge PATH                  Delete an entire directory tree
  rmdir PATH                   Delete one empty directory
  rmdirs PATH                  Delete empty descendants, retain root
  listremotes                 List account aliases
  remote add NAME --uid ID     Bind an alias to an existing logged-in account
  remote remove NAME          Remove alias only
  rc mkdir|tree|quota PATH     Explicit endpoint versions of legacy commands

Paths: D:\Backup, .\Backup, \\server\share, bd:/Backup
Directory copies always copy contents, independent of a trailing slash.
Remote names must contain at least two characters to avoid drive-letter ambiguity.

Options:
  --dry-run, -n               Preview without changing file contents
  --include PATTERN           Include matching files (repeatable)
  --exclude PATTERN           Exclude matching files (repeatable; takes precedence)
  --files-from FILE           Select relative paths from a UTF-8 text file
  --checksum                 Verify copied contents by MD5, including remote downloads
  --size-only                Compare only size (move still verifies content before deleting)
  --ignore-existing          Skip destination files that already exist
  --update                   Skip files with a newer destination timestamp
  --transfers N              Concurrent transfers (default 4)
  --checkers N               Concurrent comparisons (default 4)
  --retries N                Extra transfer attempts (default 2)
  --bwlimit RATE             Aggregate transfer budget, e.g. 1M, 512K, 1048576
  --max-delete N             Maximum file deletions (default unlimited)
  --delete-excluded          Also delete excluded destination files during sync
  --create-empty-src-dirs    Copy empty source directories
  --delete-empty-src-dirs    Remove empty source descendants after move
  --recursive, -R            Recursive listing
  --depth N                  Tree depth (default unlimited)
  --files-only|--dirs-only   Limit list output
  --log-level LEVEL          DEBUG, INFO, ERROR or OFF (default INFO)
  --log-file FILE            Write logs to this file instead of stderr
  --use-json-log             JSON lines for logs
  --progress, -P             Show periodic file progress and completion statistics

By default comparisons hash equal-sized files. Remote hashes require downloading
content because Baidu metadata MD5 is not consistently trustworthy. --size-only
is faster but weaker. New commands return nonzero on errors and check differences.
`

type parsed struct {
	Options
	Args, Includes, Excludes                                []string
	FilesFrom, LogFile, LogLevel, UID                       string
	Recursive, FilesOnly, DirsOnly, Progress, JSONLog, Help bool
	Depth                                                   int
	Seen                                                    map[string]bool
}

func parse(args []string) (parsed, error) {
	p := parsed{Options: Options{Transfers: 4, Checkers: 4, Retries: 2, MaxDelete: -1}, Depth: -1, LogLevel: "INFO", Seen: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.Args = append(p.Args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			p.Args = append(p.Args, a)
			continue
		}
		name, val, has := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch name {
		case "n":
			name = "dry-run"
		case "P":
			name = "progress"
		case "R":
			name = "recursive"
		case "h":
			name = "help"
		}
		p.Seen[name] = true
		bools := map[string]*bool{"dry-run": &p.DryRun, "checksum": &p.Checksum, "size-only": &p.SizeOnly, "ignore-existing": &p.IgnoreExisting, "update": &p.Update, "create-empty-src-dirs": &p.CreateEmptyDirs, "delete-empty-src-dirs": &p.DeleteEmptyDirs, "delete-excluded": &p.DeleteExcluded, "recursive": &p.Recursive, "files-only": &p.FilesOnly, "dirs-only": &p.DirsOnly, "progress": &p.Progress, "use-json-log": &p.JSONLog, "help": &p.Help}
		if target, ok := bools[name]; ok {
			v := true
			if has {
				var err error
				v, err = strconv.ParseBool(val)
				if err != nil {
					return p, err
				}
			}
			*target = v
			continue
		}
		if !has {
			i++
			if i >= len(args) {
				return p, fmt.Errorf("missing value for --%s", name)
			}
			val = args[i]
		}
		switch name {
		case "include":
			p.Includes = append(p.Includes, val)
		case "exclude":
			p.Excludes = append(p.Excludes, val)
		case "files-from":
			p.FilesFrom = val
		case "log-file":
			p.LogFile = val
		case "log-level":
			p.LogLevel = strings.ToUpper(val)
		case "uid":
			p.UID = val
		case "bwlimit":
			var err error
			p.BWLimit, err = parseRate(val)
			if err != nil {
				return p, err
			}
		case "transfers", "checkers", "retries", "max-delete", "depth":
			n, err := strconv.Atoi(val)
			if err != nil {
				return p, err
			}
			switch name {
			case "transfers":
				p.Transfers = n
			case "checkers":
				p.Checkers = n
			case "retries":
				p.Retries = n
			case "max-delete":
				p.MaxDelete = n
			case "depth":
				p.Depth = n
			}
		default:
			return p, fmt.Errorf("unknown option --%s", name)
		}
	}
	if p.Transfers < 1 || p.Checkers < 1 || p.Transfers > 64 || p.Checkers > 64 || p.Retries < 0 || p.MaxDelete < -1 || p.Depth < -1 {
		return p, errors.New("invalid concurrency, retries, depth or deletion limit")
	}
	if p.Checksum && p.SizeOnly {
		return p, errors.New("--checksum and --size-only are mutually exclusive")
	}
	if p.FilesOnly && p.DirsOnly {
		return p, errors.New("--files-only and --dirs-only are mutually exclusive")
	}
	switch p.LogLevel {
	case "DEBUG", "INFO", "ERROR", "OFF":
	default:
		return p, errors.New("invalid log level")
	}
	p.ShowProgress = p.Progress
	return p, nil
}
func parseRate(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := float64(1)
	for _, suffix := range []struct {
		s string
		n float64
	}{{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}} {
		if strings.HasSuffix(s, suffix.s) {
			s = strings.TrimSuffix(s, suffix.s)
			mult = suffix.n
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v*mult > float64(1<<62) {
		return 0, errors.New("invalid --bwlimit")
	}
	return int64(v * mult), nil
}

func Handles(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "rc", "__rc_transfer", "copy", "copyto", "sync", "move", "moveto", "check", "lsf", "lsjson", "lsd", "size", "delete", "deletefile", "purge", "rmdir", "rmdirs", "listremotes", "remote":
		return true
	case "mkdir", "tree", "quota":
		for _, s := range args[1:] {
			if IsRemote(s) || filepath.VolumeName(s) != "" {
				return true
			}
		}
	}
	return false
}
func Run(args []string, cwd string) int {
	if len(args) > 0 && args[0] == "__rc_transfer" {
		if err := RunWorker(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "transfer worker failed")
			return 1
		}
		return 0
	}
	if len(args) > 0 && args[0] == "rc" {
		args = args[1:]
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Print(Help)
		return 0
	}
	p, err := parse(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if p.Help {
		fmt.Print(Help)
		return 0
	}
	if err = validateFlags(args[0], p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	logWriter := io.Writer(os.Stderr)
	var logFile *os.File
	if p.LogFile != "" {
		logPath := p.LogFile
		if !filepath.IsAbs(logPath) {
			logPath = filepath.Join(cwd, logPath)
		}
		logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer logFile.Close()
		logWriter = logFile
	}
	var logMu sync.Mutex
	p.Emit = func(kind, item string) {
		if p.LogLevel == "OFF" || (p.LogLevel == "ERROR" && kind != "error") {
			return
		}
		logMu.Lock()
		defer logMu.Unlock()
		if p.JSONLog {
			json.NewEncoder(logWriter).Encode(map[string]interface{}{"time": time.Now().UTC().Format(time.RFC3339), "event": kind, "path": item})
		} else {
			fmt.Fprintf(logWriter, "%s: %s\n", kind, item)
		}
	}
	if p.FilesFrom != "" && !filepath.IsAbs(p.FilesFrom) {
		p.FilesFrom = filepath.Join(cwd, p.FilesFrom)
	}
	p.Filter, err = NewFilter(p.Includes, p.Excludes, p.FilesFrom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	resolver, err := NewResolver(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	err = runCommand(ctx, args[0], p, resolver, os.Stdout)
	if err != nil {
		if p.JSONLog {
			json.NewEncoder(logWriter).Encode(map[string]string{"event": "error", "message": err.Error()})
		} else {
			fmt.Fprintln(logWriter, err)
		}
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}
func validateFlags(cmd string, p parsed) error {
	groups := map[string]string{
		"dry-run":    "copy copyto sync move moveto delete deletefile purge rmdir rmdirs mkdir",
		"include":    "copy sync move check lsf lsjson lsd size tree delete",
		"exclude":    "copy sync move check lsf lsjson lsd size tree delete",
		"files-from": "copy sync move check lsf lsjson lsd size tree delete",
		"checksum":   "copy copyto sync move moveto check", "size-only": "copy copyto sync move moveto check",
		"ignore-existing": "copy copyto sync move moveto", "update": "copy copyto sync move moveto",
		"transfers": "copy copyto sync move moveto", "checkers": "copy sync move", "retries": "copy copyto sync move moveto", "bwlimit": "copy copyto sync move moveto",
		"max-delete": "sync delete deletefile purge", "delete-excluded": "sync", "create-empty-src-dirs": "copy sync move", "delete-empty-src-dirs": "move",
		"recursive": "lsf lsjson lsd", "depth": "tree", "files-only": "lsf lsjson", "dirs-only": "lsf lsjson", "uid": "remote",
	}
	for flag := range p.Seen {
		if allowed, ok := groups[flag]; ok && !strings.Contains(" "+allowed+" ", " "+cmd+" ") {
			return fmt.Errorf("--%s is not supported by %s", flag, cmd)
		}
	}
	return nil
}
func runCommand(ctx context.Context, cmd string, p parsed, r *Resolver, out io.Writer) error {
	if cmd == "listremotes" {
		if len(p.Args) != 0 {
			return errors.New("listremotes takes no paths")
		}
		names := make([]string, 0, len(r.Aliases))
		for n := range r.Aliases {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintln(out, n+":")
		}
		return nil
	}
	if cmd == "remote" {
		if len(p.Args) != 2 {
			return errors.New("usage: remote add NAME --uid ID | remote remove NAME")
		}
		name := p.Args[1]
		if !aliasPattern.MatchString(name) {
			return errors.New("remote names need at least two letters/digits, starting with a letter")
		}
		switch p.Args[0] {
		case "add":
			if _, exists := r.Aliases[name]; exists {
				return errors.New("alias already exists")
			}
			uid, err := strconv.ParseUint(p.UID, 10, 64)
			if err != nil {
				return errors.New("--uid must identify a logged-in account")
			}
			found := false
			for _, u := range pcsconfig.Config.BaiduUserList {
				if u != nil && u.UID == uid {
					found = true
				}
			}
			if !found {
				return errors.New("account is not logged in")
			}
			r.Aliases[name] = uid
		case "remove":
			if _, ok := r.Aliases[name]; !ok {
				return errors.New("unknown remote")
			}
			delete(r.Aliases, name)
		default:
			return errors.New("unknown remote operation")
		}
		return r.Save()
	}
	two := cmd == "copy" || cmd == "copyto" || cmd == "sync" || cmd == "move" || cmd == "moveto" || cmd == "check"
	want := 1
	if two {
		want = 2
	}
	if len(p.Args) != want {
		return fmt.Errorf("%s requires %d path arguments", cmd, want)
	}
	src, err := r.Resolve(p.Args[0])
	if err != nil {
		return err
	}
	if two {
		dst, err := r.Resolve(p.Args[1])
		if err != nil {
			return err
		}
		if cmd == "check" {
			return Check(ctx, src, dst, p.Options)
		}
		var plan *Plan
		item, err := src.FS.Stat(ctx, src.Root)
		if err != nil {
			return err
		}
		if cmd == "copyto" || cmd == "moveto" {
			plan, err = FilePlan(ctx, src, dst, cmd == "moveto", p.Options)
		} else if !item.IsDir {
			if cmd == "sync" {
				return errors.New("sync expects a source directory")
			}
			if !p.Filter.Match(item.Name) {
				return nil
			}
			dst.Root = dst.At(item.Name)
			plan, err = FilePlan(ctx, src, dst, cmd == "move", p.Options)
		} else {
			plan, err = BuildPlan(ctx, src, dst, cmd, p.Options)
		}
		if err != nil {
			return err
		}
		res, err := Execute(ctx, plan, p.Options)
		if p.Progress {
			logAction(p.Options, "summary", fmt.Sprintf("copied=%d skipped=%d deleted=%d failed=%d bytes=%d", res.Copied, res.Skipped, res.Deleted, res.Failed, res.Bytes))
		}
		return err
	}
	switch cmd {
	case "mkdir":
		if p.DryRun {
			logAction(p.Options, "would-mkdir", src.Root)
			return nil
		}
		return src.FS.Mkdir(ctx, src.Root)
	case "quota":
		remote, ok := src.FS.(*Remote)
		if !ok {
			return errors.New("quota requires a remote")
		}
		total, used, err := remote.PCS.QuotaInfo()
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]int64{"total": total, "used": used, "free": total - used})
	case "delete", "deletefile", "purge", "rmdir", "rmdirs":
		return Delete(ctx, src, cmd, p.Options)
	case "lsf", "lsjson", "lsd", "size", "tree":
		items, err := Listing(ctx, src, p.Recursive || cmd == "size" || cmd == "tree", p.Depth, p.Filter)
		if err != nil {
			return err
		}
		var count, size int64
		filtered := make([]Entry, 0, len(items))
		for _, e := range items {
			if cmd == "lsd" && !p.Filter.Match(e.Path) {
				continue
			}
			if !e.IsDir {
				count++
				size += e.Size
			}
			if (p.FilesOnly && e.IsDir) || ((p.DirsOnly || cmd == "lsd") && !e.IsDir) {
				continue
			}
			filtered = append(filtered, e)
		}
		if cmd == "lsjson" {
			return json.NewEncoder(out).Encode(filtered)
		}
		if cmd == "size" {
			return json.NewEncoder(out).Encode(map[string]int64{"count": count, "bytes": size})
		}
		for _, e := range filtered {
			n := e.Path
			if e.IsDir {
				n += "/"
			}
			if cmd == "tree" {
				fmt.Fprintln(out, strings.Repeat("  ", strings.Count(e.Path, "/"))+e.Name+map[bool]string{true: "/"}[e.IsDir])
			} else {
				fmt.Fprintln(out, n)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown rc command %q", cmd)
	}
}
