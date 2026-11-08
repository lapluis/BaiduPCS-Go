package pcsclone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checkedwriter"
)

var errHelpRequested = errors.New("help requested")

type flagKind int

const (
	flagBool flagKind = iota
	flagString
	flagInt
	flagList
)

type flagSpec struct {
	Name  string
	Short string
	Value string
	Usage string
	Kind  flagKind
}

// Left renders the option column in command help.
func (s flagSpec) Left() string {
	left := "--" + s.Name
	if s.Short != "" {
		left += ", -" + s.Short
	}
	if s.Kind != flagBool {
		left += " " + s.Value
	}
	return left
}

func boolFlag(name, short, usage string) flagSpec {
	return flagSpec{Name: name, Short: short, Usage: usage, Kind: flagBool}
}

func stringFlag(name, short, value, usage string) flagSpec {
	return flagSpec{Name: name, Short: short, Value: value, Usage: usage, Kind: flagString}
}

func intFlag(name, short, value, usage string) flagSpec {
	return flagSpec{Name: name, Short: short, Value: value, Usage: usage, Kind: flagInt}
}

func listFlag(name, value, usage string) flagSpec {
	return flagSpec{Name: name, Value: value, Usage: usage, Kind: flagList}
}

type flagValues struct {
	values map[string]interface{}
	set    map[string]bool
}

func (v *flagValues) Has(name string) bool { return v.set[name] }

func (v *flagValues) Bool(name string) bool {
	b, _ := v.values[name].(bool)
	return b
}

func (v *flagValues) String(name string) string {
	s, _ := v.values[name].(string)
	return s
}

func (v *flagValues) Int(name string) int {
	n, _ := v.values[name].(int)
	return n
}

func (v *flagValues) IntOr(name string, fallback int) int {
	if !v.set[name] {
		return fallback
	}
	return v.Int(name)
}

func (v *flagValues) List(name string) []string {
	list, _ := v.values[name].([]string)
	return list
}

func parseCommandFlags(args []string, specs []flagSpec) ([]string, *flagValues, error) {
	byName := make(map[string]flagSpec, len(specs))
	byShort := make(map[string]flagSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
		if spec.Short != "" {
			byShort[spec.Short] = spec
		}
	}

	values := &flagValues{values: map[string]interface{}{}, set: map[string]bool{}}
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if arg == "-h" || arg == "--help" {
			return nil, nil, errHelpRequested
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		var (
			spec   flagSpec
			value  string
			hasVal bool
			ok     bool
		)
		if strings.HasPrefix(arg, "--") {
			name, v, has := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			spec, ok = byName[name]
			if !ok {
				return nil, nil, usageErrorf("unknown option --%s; run 'pcsclone help' for options", name)
			}
			value, hasVal = v, has
		} else {
			spec, ok = byShort[strings.TrimPrefix(arg, "-")]
			if !ok {
				return nil, nil, usageErrorf("unknown option %s; run 'pcsclone help' for options", diagnosticName(arg))
			}
		}

		if spec.Kind == flagBool {
			b := true
			if hasVal {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return nil, nil, usageErrorf("invalid value %q for --%s", value, spec.Name)
				}
				b = parsed
			}
			values.values[spec.Name] = b
			values.set[spec.Name] = true
			continue
		}
		if !hasVal {
			i++
			if i >= len(args) {
				return nil, nil, usageErrorf("missing value for --%s", spec.Name)
			}
			value = args[i]
		}
		switch spec.Kind {
		case flagString:
			if spec.Name == "files-from" && strings.TrimSpace(value) == "" {
				return nil, nil, usageErrorf("missing value for --files-from")
			}
			values.values[spec.Name] = value
		case flagList:
			values.values[spec.Name] = append(values.List(spec.Name), value)
		case flagInt:
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, nil, usageErrorf("invalid integer %q for --%s", value, spec.Name)
			}
			values.values[spec.Name] = n
		}
		values.set[spec.Name] = true
	}
	return positional, values, nil
}

func parseRate(s string) (int64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	return rc.ParseBandwidthRate(s)
}

type logSink struct {
	w            io.Writer
	file         io.Closer
	output       *checkedwriter.Writer
	closed       bool
	closeErr     error
	level        string
	json         bool
	mu           sync.Mutex
	terminal     bool
	lines        int
	lastProgress time.Time
}

type logOutputError struct{ err error }

func (e *logOutputError) Error() string { return "log output: " + e.err.Error() }
func (e *logOutputError) Unwrap() error { return e.err }

var openLogFile = func(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
}

// writer is called while holding mu, after terminal detection has completed.
func (s *logSink) writer() io.Writer {
	if s.output == nil {
		s.output = checkedwriter.New(s.w)
	}
	return s.output
}

func newLogSink(c *commandContext) (*logSink, error) {
	level := strings.ToUpper(c.flags.String("log-level"))
	if level == "" {
		level = "INFO"
	}
	switch level {
	case "DEBUG", "INFO", "ERROR", "OFF":
	default:
		return nil, usageErrorf("invalid --log-level %q", level)
	}
	sink := &logSink{w: c.app.Stderr, level: level, json: c.flags.Bool("use-json-log")}
	if path := c.flags.String("log-file"); path != "" {
		ctx := c.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := rc.ValidateCommandLogPath(ctx, c.app.CWD, path, c.app.ConfigFile, c.flags.String("files-from"), c.args...); err != nil {
			return nil, err
		}
		path, err := rc.ResolveLocalPath(c.app.CWD, path)
		if err != nil {
			return nil, err
		}
		f, err := openLogFile(path)
		if err != nil {
			return nil, err
		}
		sink.file = f
		sink.w = f
	}
	if f, ok := sink.w.(*os.File); ok && sink.file == nil && !sink.json && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())) {
		sink.terminal = true
		sink.w = colorable.NewColorable(f)
	}
	return sink, nil
}

func (s *logSink) clearProgress() {
	if s.lines == 0 {
		return
	}
	w := s.writer()
	fmt.Fprintf(w, "\x1b[%dA", s.lines)
	for i := 0; i < s.lines; i++ {
		fmt.Fprint(w, "\r\x1b[2K\n")
	}
	fmt.Fprintf(w, "\x1b[%dA", s.lines)
	s.lines = 0
}

func (s *logSink) Progress(p rc.ProgressSnapshot) {
	if !s.terminal {
		s.emit("progress", p.String(), p.Final)
		return
	}
	if s.level == "OFF" || s.level == "ERROR" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.clearProgress()
	lines := p.Lines()
	w := s.writer()
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	s.lines = len(lines)
}

func (s *logSink) Emit(kind, item string) {
	s.emit(kind, item, false)
}

func (s *logSink) emit(kind, item string, force bool) {
	if s == nil || s.level == "OFF" || (s.level == "ERROR" && kind != "error") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if kind == "progress" && !s.terminal {
		if !force && time.Since(s.lastProgress) < 5*time.Second {
			return
		}
		s.lastProgress = time.Now()
	}
	s.clearProgress()
	if s.terminal && (kind == "copy" || kind == "skip") {
		return
	}
	if s.json {
		_ = json.NewEncoder(s.writer()).Encode(map[string]interface{}{
			"time":  time.Now().UTC().Format(time.RFC3339),
			"event": kind,
			"path":  item,
		})
		return
	}
	fmt.Fprintf(s.writer(), "%s: %s\n", kind, item)
}

func (s *logSink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.clearProgress()
	s.closed = true
	var err error
	if s.file != nil {
		err = s.file.Close()
	}
	if s.output != nil {
		err = errors.Join(s.output.Err(), err)
	}
	if err != nil {
		s.closeErr = &logOutputError{err: err}
	}
	return s.closeErr
}

func (s *logSink) finish(operationErr error) error {
	if err := s.Close(); err != nil {
		return errors.Join(operationErr, err)
	}
	return operationErr
}

// filter reads only the selection flags shared by list-style commands.
func (c *commandContext) filter() (rc.Filter, error) {
	filesFrom := c.flags.String("files-from")
	if filesFrom != "" {
		var err error
		filesFrom, err = rc.ResolveLocalPath(c.app.CWD, filesFrom)
		if err != nil {
			return rc.Filter{}, usageErrorf("%s", err)
		}
	}
	f, err := rc.NewFilter(c.flags.List("include"), c.flags.List("exclude"), filesFrom)
	if err != nil {
		return f, usageErrorf("%s", err)
	}
	return f, nil
}

// options builds the shared execution options and log sink.
func (c *commandContext) options() (rc.Options, *logSink, error) {
	sink, err := newLogSink(c)
	if err != nil {
		if _, ok := err.(*exitError); ok {
			return rc.Options{}, nil, err
		}
		return rc.Options{}, nil, operationError(err)
	}
	f, err := c.filter()
	if err != nil {
		return rc.Options{}, nil, sink.finish(err)
	}
	o := rc.Options{
		DryRun:          c.flags.Bool("dry-run"),
		Checksum:        c.flags.Bool("checksum"),
		SizeOnly:        c.flags.Bool("size-only"),
		IgnoreExisting:  c.flags.Bool("ignore-existing"),
		Update:          c.flags.Bool("update"),
		CreateEmptyDirs: c.flags.Bool("create-empty-src-dirs"),
		DeleteEmptyDirs: c.flags.Bool("delete-empty-src-dirs"),
		DeleteExcluded:  c.flags.Bool("delete-excluded"),
		Transfers:       c.flags.IntOr("transfers", 4),
		DownloadThreads: c.flags.IntOr("download-threads", 4),
		MaxConnections:  c.flags.IntOr("max-connections", 16),
		Checkers:        c.flags.IntOr("checkers", 4),
		Retries:         c.flags.IntOr("retries", 2),
		MaxDelete:       c.flags.IntOr("max-delete", -1),
		ShowProgress:    c.flags.Bool("progress"),
		Filter:          f,
		Emit:            sink.Emit,
		Progress:        sink.Progress,
	}
	o.BWLimit, err = parseRate(c.flags.String("bwlimit"))
	if err != nil {
		return rc.Options{}, nil, sink.finish(usageErrorf("%s", err))
	}
	if o.Transfers < 1 || o.Transfers > 64 || o.Checkers < 1 || o.Checkers > 64 || o.Retries < 0 || o.MaxDelete < -1 || o.DownloadThreads < 1 || o.DownloadThreads > 32 || o.MaxConnections < 1 || o.MaxConnections > 256 || o.Transfers > o.MaxConnections {
		return rc.Options{}, nil, sink.finish(usageErrorf("invalid concurrency, retries or deletion limit"))
	}
	if o.Checksum && o.SizeOnly {
		return rc.Options{}, nil, sink.finish(usageErrorf("--checksum and --size-only are mutually exclusive"))
	}
	return o, sink, nil
}
