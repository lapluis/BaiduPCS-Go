package pcsclone

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
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
				return nil, nil, usageErrorf("unknown option %s; run 'pcsclone help' for options", arg)
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
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}
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

type logSink struct {
	w     io.Writer
	file  *os.File
	level string
	json  bool
	mu    sync.Mutex
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
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.app.CWD, path)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		sink.file = f
		sink.w = f
	}
	return sink, nil
}

func (s *logSink) Emit(kind, item string) {
	if s == nil || s.level == "OFF" || (s.level == "ERROR" && kind != "error") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.json {
		_ = json.NewEncoder(s.w).Encode(map[string]interface{}{
			"time":  time.Now().UTC().Format(time.RFC3339),
			"event": kind,
			"path":  item,
		})
		return
	}
	fmt.Fprintf(s.w, "%s: %s\n", kind, item)
}

func (s *logSink) Close() error {
	if s != nil && s.file != nil {
		return s.file.Close()
	}
	return nil
}

// filter reads only the selection flags shared by list-style commands.
func (c *commandContext) filter() (rc.Filter, error) {
	filesFrom := c.flags.String("files-from")
	if filesFrom != "" && !filepath.IsAbs(filesFrom) {
		filesFrom = filepath.Join(c.app.CWD, filesFrom)
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
		sink.Close()
		return rc.Options{}, nil, err
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
		Checkers:        c.flags.IntOr("checkers", 4),
		Retries:         c.flags.IntOr("retries", 2),
		MaxDelete:       c.flags.IntOr("max-delete", -1),
		ShowProgress:    c.flags.Bool("progress"),
		Filter:          f,
		Emit:            sink.Emit,
	}
	o.BWLimit, err = parseRate(c.flags.String("bwlimit"))
	if err != nil {
		sink.Close()
		return rc.Options{}, nil, usageErrorf("%s", err)
	}
	if o.Transfers < 1 || o.Transfers > 64 || o.Checkers < 1 || o.Checkers > 64 || o.Retries < 0 || o.MaxDelete < -1 {
		sink.Close()
		return rc.Options{}, nil, usageErrorf("invalid concurrency, retries or deletion limit")
	}
	if o.Checksum && o.SizeOnly {
		sink.Close()
		return rc.Options{}, nil, usageErrorf("--checksum and --size-only are mutually exclusive")
	}
	return o, sink, nil
}
