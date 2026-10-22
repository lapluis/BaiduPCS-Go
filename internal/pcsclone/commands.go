package pcsclone

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

var (
	commandsOnce sync.Once
	allCommands  []*command
)

func topLevelCommands() []*command {
	commandsOnce.Do(func() {
		allCommands = buildCommands()
	})
	return allCommands
}

func buildCommands() []*command {
	return []*command{
		&command{
			Name: "copy", Category: "Core file operations",
			Summary: "Copy directory contents (keep extra destination files)",
			Usage:   "pcsclone copy SOURCE DEST [options]",
			Description: `Copy directory contents or place a source file in a destination
directory. A trailing slash never changes the meaning. Extra destination files
are retained.`,
			MinArgs: 2, MaxArgs: 2, Flags: copyFlags(), Run: transferCommand("copy"),
		},
		&command{
			Name: "copyto", Category: "Core file operations",
			Summary: "Copy one file to an exact target filename",
			Usage:   "pcsclone copyto FILE TARGET [options]",
			Description: `Copy a single file to TARGET, which is a filename rather than a
destination directory.`,
			MinArgs: 2, MaxArgs: 2, Flags: fileTransferFlags(), Run: transferCommand("copyto"),
		},
		&command{
			Name: "sync", Category: "Core file operations",
			Summary: "Mirror source contents and delete extra destination files",
			Usage:   "pcsclone sync SOURCE DEST [options]",
			Description: `Make DEST contain exactly the filter-selected contents of SOURCE.
Destination-only files are deleted only after scanning and transfers succeed.`,
			MinArgs: 2, MaxArgs: 2, Flags: syncFlags(), Run: transferCommand("sync"),
		},
		&command{
			Name: "move", Category: "Core file operations",
			Summary: "Move directory contents after verifying each copy",
			Usage:   "pcsclone move SOURCE DEST [options]",
			Description: `Copy each source file, verify its content, then remove the source.
A failed copy or verification always retains the source file.`,
			MinArgs: 2, MaxArgs: 2, Flags: moveFlags(), Run: transferCommand("move"),
		},
		&command{
			Name: "moveto", Category: "Core file operations",
			Summary: "Move one file to an exact target filename",
			Usage:   "pcsclone moveto FILE TARGET [options]",
			Description: `Move a single file to TARGET, which is a filename rather than a
destination directory. Verification happens before source removal.`,
			MinArgs: 2, MaxArgs: 2, Flags: fileTransferFlags(), Run: transferCommand("moveto"),
		},
		&command{
			Name: "check", Category: "Core file operations",
			Summary: "Compare two directory trees without changing files",
			Usage:   "pcsclone check SOURCE DEST [options]",
			Description: `Compare files under SOURCE and DEST. Content MD5 is compared
unless --size-only is used; remote hashes are computed by downloading content.
Exits nonzero when any difference is found.`,
			MinArgs: 2, MaxArgs: 2, Flags: checkFlags(), Run: transferCommand("check"),
		},
		&command{
			Name: "ls", Category: "Core file operations",
			Summary: "List paths recursively (default)",
			Usage:   "pcsclone ls PATH [options]",
			Description: `List PATH recursively by default. Use --recursive=false for one
level. Directories are printed with a trailing slash.`,
			MinArgs: 1, MaxArgs: 1, Flags: listFlags(true), Run: listCommand("ls"),
		},
		&command{
			Name: "lsf", Category: "Core file operations",
			Summary: "List paths one level deep",
			Usage:   "pcsclone lsf PATH [options]",
			Description: `List PATH one level deep by default. Add --recursive/-R for all
descendants. Directories are printed with a trailing slash.`,
			MinArgs: 1, MaxArgs: 1, Flags: listFlags(false), Run: listCommand("lsf"),
		},
		&command{
			Name: "lsd", Category: "Core file operations",
			Summary: "List directories",
			Usage:   "pcsclone lsd PATH [options]",
			Description: `List directory entries one level deep by default. Add
--recursive/-R for descendants.`,
			MinArgs: 1, MaxArgs: 1, Flags: lsDFlags(), Run: listCommand("lsd"),
		},
		&command{
			Name: "lsjson", Category: "Core file operations",
			Summary: "Write a JSON array of entries",
			Usage:   "pcsclone lsjson PATH [options]",
			Description: `Write entries as a JSON array containing Path, Name, Size,
ModTime and IsDir. One level is listed by default; use --recursive/-R for all
descendants.`,
			MinArgs: 1, MaxArgs: 1, Flags: listFlags(false), Run: listCommand("lsjson"),
		},
		&command{
			Name: "stat", Category: "Core file operations",
			Summary: "Show one object as a JSON object",
			Usage:   "pcsclone stat PATH [options]",
			Description: `Show metadata for exactly one file or directory as a JSON object.
--hash computes the content MD5; for a remote file this downloads its content.`,
			MinArgs: 1, MaxArgs: 1, Flags: []flagSpec{
				boolFlag("hash", "", "Compute and include the content MD5 (remote: downloads content)"),
			},
			Run: runStat,
		},
		&command{
			Name: "size", Category: "Core file operations",
			Summary: "Count files and bytes recursively",
			Usage:   "pcsclone size PATH [options]",
			Description: `Count filter-selected files and their total size recursively.
Text is printed by default; --json writes {"count":N,"bytes":N}.`,
			MinArgs: 1, MaxArgs: 1, Flags: []flagSpec{
				listFlag("include", "PATTERN", "Include matching files (repeatable)"),
				listFlag("exclude", "PATTERN", "Exclude matching files (repeatable; wins)"),
				stringFlag("files-from", "", "FILE", "Select relative paths from a UTF-8 text file"),
				boolFlag("json", "", "Write a JSON object"),
			},
			Run: runSize,
		},
		&command{
			Name: "about", Category: "Core file operations",
			Summary: "Show account quota for a remote",
			Usage:   "pcsclone about REMOTE: [options]",
			Description: `Show total, used and free bytes for the account selected by a
remote path such as bd: or bd:/backup. Text is printed by default; --json writes
{"total":N,"used":N,"free":N}.`,
			MinArgs: 1, MaxArgs: 1, Flags: []flagSpec{
				boolFlag("json", "", "Write a JSON object"),
			},
			Run: runAbout,
		},
		&command{
			Name: "delete", Category: "Core file operations",
			Summary: "Delete filter-selected files, keeping directories",
			Usage:   "pcsclone delete PATH [options]",
			Description: `Delete files under PATH that match the filters. Directories are
retained. All candidates are rechecked before the first deletion.`,
			MinArgs: 1, MaxArgs: 1, Flags: deleteFlags(true), Run: deleteCommand("delete"),
		},
		&command{
			Name: "deletefile", Category: "Core file operations",
			Summary: "Delete exactly one file",
			Usage:   "pcsclone deletefile FILE [options]",
			MinArgs: 1, MaxArgs: 1, Flags: deleteFlags(false), Run: deleteCommand("deletefile"),
		},
		&command{
			Name: "purge", Category: "Core file operations",
			Summary: "Delete a directory tree and all contents",
			Usage:   "pcsclone purge PATH [options]",
			Description: `Delete PATH and everything below it. Filesystem roots are
refused. Filters are not accepted.`,
			MinArgs: 1, MaxArgs: 1, Flags: deleteFlags(false), Run: deleteCommand("purge"),
		},
		&command{
			Name: "rmdir", Category: "Core file operations",
			Summary: "Delete one empty directory",
			Usage:   "pcsclone rmdir PATH [options]",
			MinArgs: 1, MaxArgs: 1, Flags: simpleChangeFlags(), Run: deleteCommand("rmdir"),
		},
		&command{
			Name: "rmdirs", Category: "Core file operations",
			Summary:     "Delete empty descendant directories",
			Usage:       "pcsclone rmdirs PATH [options]",
			Description: `Remove empty directories below PATH while retaining PATH itself.`,
			MinArgs:     1, MaxArgs: 1, Flags: simpleChangeFlags(), Run: deleteCommand("rmdirs"),
		},
		&command{
			Name: "mkdir", Category: "Core file operations",
			Summary: "Create a directory including missing parents",
			Usage:   "pcsclone mkdir PATH [options]",
			MinArgs: 1, MaxArgs: 1, Flags: simpleChangeFlags(), Run: runMkdir,
		},
		&command{
			Name: "tree", Category: "Core file operations",
			Summary: "Print a directory tree",
			Usage:   "pcsclone tree PATH [options]",
			Description: `Print a tree rooted at PATH. --depth 1 lists direct children;
the default is unlimited.`,
			MinArgs: 1, MaxArgs: 1, Flags: []flagSpec{
				listFlag("include", "PATTERN", "Include matching files (repeatable)"),
				listFlag("exclude", "PATTERN", "Exclude matching files (repeatable; wins)"),
				stringFlag("files-from", "", "FILE", "Select relative paths from a UTF-8 text file"),
				intFlag("depth", "", "N", "Maximum depth (default unlimited)"),
			},
			Run: listCommand("tree"),
		},
		buildConfigGroup(),
		&command{
			Name: "listremotes", Category: "Account configuration",
			Summary: "List configured account aliases",
			Usage:   "pcsclone listremotes",
			Description: `List configured remote names, one per line, each followed by a
colon. No credentials are printed.`,
			Run: runListRemotes,
		},
		buildBaiduGroup(),
		completionCommand(),
	}
}

func transferCommand(mode string) func(*commandContext) error {
	return func(c *commandContext) error { return runTransfer(c, mode) }
}

func listCommand(mode string) func(*commandContext) error {
	return func(c *commandContext) error { return runList(c, mode) }
}

func deleteCommand(mode string) func(*commandContext) error {
	return func(c *commandContext) error { return runDelete(c, mode) }
}

// Shared option groups. Keeping the slices fresh avoids accidental aliasing
// when several commands append to them.
func filterFlags() []flagSpec {
	return []flagSpec{
		listFlag("include", "PATTERN", "Include matching files (repeatable)"),
		listFlag("exclude", "PATTERN", "Exclude matching files (repeatable; wins)"),
		stringFlag("files-from", "", "FILE", "Select relative paths from a UTF-8 text file"),
	}
}

func logFlags() []flagSpec {
	return []flagSpec{
		stringFlag("log-level", "", "LEVEL", "DEBUG, INFO, ERROR or OFF (default INFO)"),
		stringFlag("log-file", "", "FILE", "Write event logs to FILE instead of stderr"),
		boolFlag("use-json-log", "", "Write event logs as JSON lines"),
	}
}

func transferBaseFlags() []flagSpec {
	flags := []flagSpec{
		boolFlag("dry-run", "n", "Preview without changing file contents"),
		boolFlag("checksum", "", "Verify content MD5 after each transfer"),
		boolFlag("size-only", "", "Compare only file size (move still verifies)"),
		boolFlag("ignore-existing", "", "Skip destination files that already exist"),
		boolFlag("update", "", "Skip files whose destination timestamp is newer"),
		intFlag("transfers", "", "N", "Concurrent transfers (default 4, 1-64)"),
		intFlag("retries", "", "N", "Extra transfer attempts (default 2)"),
		stringFlag("bwlimit", "", "RATE", "Aggregate bandwidth, e.g. 1M, 512K, 1048576"),
		boolFlag("progress", "P", "Show periodic progress and a final summary"),
	}
	return append(flags, logFlags()...)
}

func copyFlags() []flagSpec {
	flags := append(filterFlags(), transferBaseFlags()...)
	flags = append(flags, intFlag("checkers", "", "N", "Concurrent comparisons (default 4, 1-64)"))
	flags = append(flags, boolFlag("create-empty-src-dirs", "", "Copy empty source directories"))
	return flags
}

func syncFlags() []flagSpec {
	flags := copyFlags()
	flags = append(flags,
		intFlag("max-delete", "", "N", "Maximum file deletions (default unlimited)"),
		boolFlag("delete-excluded", "", "Also delete filter-excluded destination files"),
	)
	return flags
}

func moveFlags() []flagSpec {
	flags := copyFlags()
	flags = append(flags, boolFlag("delete-empty-src-dirs", "", "Remove empty source descendants after move"))
	return flags
}

func fileTransferFlags() []flagSpec {
	return transferBaseFlags()
}

func checkFlags() []flagSpec {
	flags := append(filterFlags(), boolFlag("size-only", "", "Compare only file size"))
	return append(flags, logFlags()...)
}

func listFlags(recursiveDefault bool) []flagSpec {
	usage := "Recurse into descendants"
	if recursiveDefault {
		usage = "Recurse into descendants (default true; use --recursive=false to disable)"
	}
	return []flagSpec{
		listFlag("include", "PATTERN", "Include matching files (repeatable)"),
		listFlag("exclude", "PATTERN", "Exclude matching files (repeatable; wins)"),
		stringFlag("files-from", "", "FILE", "Select relative paths from a UTF-8 text file"),
		boolFlag("recursive", "R", usage),
		boolFlag("files-only", "", "Limit output to files"),
		boolFlag("dirs-only", "", "Limit output to directories"),
	}
}

func lsDFlags() []flagSpec {
	return []flagSpec{
		listFlag("include", "PATTERN", "Include matching directories (repeatable)"),
		listFlag("exclude", "PATTERN", "Exclude matching directories (repeatable; wins)"),
		stringFlag("files-from", "", "FILE", "Select relative paths from a UTF-8 text file"),
		boolFlag("recursive", "R", "Recurse into descendants"),
	}
}

func deleteFlags(filters bool) []flagSpec {
	flags := []flagSpec{
		boolFlag("dry-run", "n", "Preview without deleting"),
		intFlag("max-delete", "", "N", "Maximum file deletions (default unlimited)"),
	}
	if filters {
		flags = append(flags, filterFlags()...)
	}
	return append(flags, logFlags()...)
}

func simpleChangeFlags() []flagSpec {
	flags := []flagSpec{boolFlag("dry-run", "n", "Preview without changing the filesystem")}
	return append(flags, logFlags()...)
}

// resolve turns a command-line path into an endpoint and adds pcsclone-specific
// guidance when the remote name is unknown.
func (c *commandContext) resolve(arg string) (rc.Endpoint, error) {
	endpoint, err := c.app.Resolver.Resolve(arg)
	if err == nil {
		return endpoint, nil
	}
	if rc.IsRemote(arg) {
		alias, _, _ := strings.Cut(arg, ":")
		if _, ok := c.app.Resolver.Aliases[alias]; !ok {
			return endpoint, fmt.Errorf("unknown remote %q; run 'pcsclone config create %s' or 'pcsclone config import-pcsgo'", alias, alias)
		}
	}
	return endpoint, err
}

func runTransfer(c *commandContext, mode string) error {
	o, sink, err := c.options()
	if err != nil {
		return err
	}
	defer sink.Close()

	src, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	dst, err := c.resolve(c.args[1])
	if err != nil {
		return operationError(err)
	}
	if mode == "check" {
		return operationError(rc.Check(c.ctx, src, dst, o))
	}

	var plan *rc.Plan
	item, err := src.FS.Stat(c.ctx, src.Root)
	if err != nil {
		return operationError(err)
	}
	switch {
	case mode == "copyto" || mode == "moveto":
		plan, err = rc.FilePlan(c.ctx, src, dst, mode == "moveto", o)
	case !item.IsDir:
		if mode == "sync" {
			return operationError(fmt.Errorf("sync expects a source directory"))
		}
		if !o.Filter.Match(item.Name) {
			return nil
		}
		dst.Root = dst.At(item.Name)
		plan, err = rc.FilePlan(c.ctx, src, dst, mode == "move", o)
	default:
		plan, err = rc.BuildPlan(c.ctx, src, dst, mode, o)
	}
	if err != nil {
		return operationError(err)
	}
	result, err := rc.Execute(c.ctx, plan, o)
	if o.ShowProgress {
		sink.Emit("summary", fmt.Sprintf("copied=%d skipped=%d deleted=%d failed=%d bytes=%d",
			result.Copied, result.Skipped, result.Deleted, result.Failed, result.Bytes))
	}
	return operationError(err)
}

func runList(c *commandContext, mode string) error {
	f, err := c.filter()
	if err != nil {
		return err
	}
	endpoint, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	recursive := c.flags.Bool("recursive")
	if mode == "ls" && !c.flags.Has("recursive") {
		recursive = true
	}
	depth := -1
	if mode == "tree" {
		recursive = true
		depth = c.flags.IntOr("depth", -1)
		if depth < -1 {
			return usageErrorf("--depth must be -1 or greater")
		}
	}
	if c.flags.Bool("files-only") && c.flags.Bool("dirs-only") {
		return usageErrorf("--files-only and --dirs-only are mutually exclusive")
	}
	items, err := rc.Listing(c.ctx, endpoint, recursive, depth, f)
	if err != nil {
		return operationError(err)
	}
	filtered := make([]rc.Entry, 0, len(items))
	for _, entry := range items {
		if mode == "lsd" {
			if !entry.IsDir || !f.Match(entry.Path) {
				continue
			}
		}
		if c.flags.Bool("files-only") && entry.IsDir {
			continue
		}
		if c.flags.Bool("dirs-only") && !entry.IsDir {
			continue
		}
		filtered = append(filtered, entry)
	}
	if mode == "lsjson" {
		return json.NewEncoder(c.app.Stdout).Encode(filtered)
	}
	for _, entry := range filtered {
		if mode == "tree" {
			name := entry.Name
			if entry.IsDir {
				name += "/"
			}
			fmt.Fprintln(c.app.Stdout, strings.Repeat("  ", strings.Count(entry.Path, "/"))+name)
			continue
		}
		name := entry.Path
		if entry.IsDir {
			name += "/"
		}
		fmt.Fprintln(c.app.Stdout, name)
	}
	return nil
}

func runSize(c *commandContext) error {
	f, err := c.filter()
	if err != nil {
		return err
	}
	endpoint, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	items, err := rc.Listing(c.ctx, endpoint, true, -1, f)
	if err != nil {
		return operationError(err)
	}
	var count, bytes int64
	for _, entry := range items {
		if !entry.IsDir {
			count++
			bytes += entry.Size
		}
	}
	if c.flags.Bool("json") {
		return json.NewEncoder(c.app.Stdout).Encode(map[string]int64{"count": count, "bytes": bytes})
	}
	fmt.Fprintf(c.app.Stdout, "Count: %d\nBytes: %d\n", count, bytes)
	return nil
}

type statResult struct {
	Path    string    `json:"Path"`
	Name    string    `json:"Name"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
	Hash    string    `json:"Hash,omitempty"`
	Remote  bool      `json:"Remote"`
	UID     uint64    `json:"UID,omitempty"`
}

func runStat(c *commandContext) error {
	endpoint, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	entry, err := endpoint.FS.Stat(c.ctx, endpoint.Root)
	if err != nil {
		return operationError(err)
	}
	result := statResult{
		Path:    c.args[0],
		Name:    entry.Name,
		Size:    entry.Size,
		ModTime: entry.ModTime,
		IsDir:   entry.IsDir,
	}
	if _, ok := endpoint.FS.(*rc.Local); ok {
		result.Path = endpoint.Root
	}
	if remote, ok := endpoint.FS.(*rc.Remote); ok {
		result.Remote = true
		result.UID = remote.UID
	}
	if c.flags.Bool("hash") {
		if entry.IsDir {
			return usageErrorf("--hash requires a file")
		}
		hash, err := endpoint.FS.Hash(c.ctx, endpoint.Root)
		if err != nil {
			return operationError(err)
		}
		result.Hash = hash
	}
	encoder := json.NewEncoder(c.app.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runAbout(c *commandContext) error {
	endpoint, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	remote, ok := endpoint.FS.(*rc.Remote)
	if !ok {
		return usageErrorf("about requires a remote path such as bd: or bd:/directory")
	}
	total, used, pcsErr := remote.PCS.QuotaInfo()
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	free := total - used
	if c.flags.Bool("json") {
		return json.NewEncoder(c.app.Stdout).Encode(map[string]int64{"total": total, "used": used, "free": free})
	}
	fmt.Fprintf(c.app.Stdout, "Total: %d\nUsed: %d\nFree: %d\n", total, used, free)
	return nil
}

func runMkdir(c *commandContext) error {
	o, sink, err := c.options()
	if err != nil {
		return err
	}
	defer sink.Close()
	endpoint, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	if o.DryRun {
		sink.Emit("would-mkdir", endpoint.Root)
		return nil
	}
	if err = endpoint.FS.Mkdir(c.ctx, endpoint.Root); err != nil {
		return operationError(err)
	}
	sink.Emit("mkdir", endpoint.Root)
	return nil
}

func runDelete(c *commandContext, mode string) error {
	o, sink, err := c.options()
	if err != nil {
		return err
	}
	defer sink.Close()
	endpoint, err := c.resolve(c.args[0])
	if err != nil {
		return operationError(err)
	}
	return operationError(rc.Delete(c.ctx, endpoint, mode, o))
}

func runListRemotes(c *commandContext) error {
	names := make([]string, 0, len(c.app.Resolver.Aliases))
	for name := range c.app.Resolver.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(c.app.Stdout, "%s:\n", name)
	}
	return nil
}
