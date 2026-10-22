// Package pcsclone implements the standalone pcsclone command line.
//
// The package deliberately keeps its parsing, help and account commands
// separate from the legacy BaiduPCS-Go CLI. File operations are executed by
// the shared internal/rc engine, and transfer workers re-exec the same binary.
package pcsclone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

const (
	// EnvConfigFile carries the selected configuration file to transfer
	// workers started by re-executing the current binary.
	EnvConfigFile = "PCSCLONE_CONFIG_FILE"
	// EnvConfigDir is the default directory used when EnvConfigFile is unset.
	EnvConfigDir = "PCSCLONE_CONFIG_DIR"

	defaultConfigName = "pcs_config.json"
)

// App holds the process-wide state for one pcsclone invocation.
type App struct {
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
	CWD     string

	ConfigFile string
	Resolver   *rc.Resolver

	initialized bool
}

// exitError attaches a process exit code to an error.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...interface{}) error {
	return &exitError{code: 2, err: fmt.Errorf(format, args...)}
}

func operationError(err error) error {
	if err == nil {
		return nil
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return err
	}
	return &exitError{code: 1, err: err}
}

// Run executes args and returns a process exit code.
//
// Codes: 0 success, 1 operation failure (including check differences), 2 usage
// error, 130 interrupted.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "pcsclone: cannot determine working directory:", err)
		return 1
	}
	app := &App{Version: version, Stdout: stdout, Stderr: stderr, CWD: cwd}
	return app.run(args)
}

func (a *App) run(raw []string) int {
	oldConfigEnv, hadConfigEnv := os.LookupEnv(EnvConfigFile)
	defer func() {
		if a.initialized {
			_ = pcsconfig.Config.Close()
		}
		if hadConfigEnv {
			_ = os.Setenv(EnvConfigFile, oldConfigEnv)
		} else {
			_ = os.Unsetenv(EnvConfigFile)
		}
	}()
	global, err := parseGlobalOptions(raw)
	if err != nil {
		return a.report(err)
	}
	if global.version {
		fmt.Fprintf(a.Stdout, "pcsclone %s\n", a.Version)
		return 0
	}
	if len(global.args) == 0 {
		a.printGlobalHelp()
		return 0
	}
	if global.help {
		a.printGlobalHelp()
		return 0
	}

	name := global.args[0]
	rest := global.args[1:]
	switch name {
	case "help":
		return a.runHelp(rest)
	case "version":
		if len(rest) != 0 {
			return a.report(usageErrorf("version takes no arguments"))
		}
		fmt.Fprintf(a.Stdout, "pcsclone %s\n", a.Version)
		return 0
	case "__rc_transfer":
		// Hidden worker entry used by internal/rc. Configuration is selected
		// through EnvConfigFile inherited from the parent process.
		if err := a.ensureRuntime(global.config); err != nil {
			fmt.Fprintln(a.Stderr, "pcsclone: transfer worker:", err)
			return 1
		}
		code := rc.Run(append([]string{name}, rest...), a.CWD)
		pcsconfig.Config.Close()
		return code
	}

	cmd := findCommand(topLevelCommands(), name)
	if cmd == nil {
		return a.report(usageErrorf("unknown command %q; run 'pcsclone help'", name))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	err = a.runCommand(ctx, cmd, rest, global.config)
	if ctx.Err() != nil {
		if err != nil {
			fmt.Fprintln(a.Stderr, "pcsclone:", err)
		}
		fmt.Fprintln(a.Stderr, "pcsclone: canceled")
		return 130
	}
	return a.report(err)
}

func (a *App) report(err error) int {
	if err == nil {
		return 0
	}
	code := 1
	var ee *exitError
	if errors.As(err, &ee) {
		code = ee.code
		err = ee.err
	}
	if err != nil {
		fmt.Fprintln(a.Stderr, "pcsclone:", err)
	}
	return code
}

// ensureRuntime selects the independent configuration file and loads aliases.
// It is idempotent for one process.
func (a *App) ensureRuntime(configPath string) error {
	if a.initialized {
		return nil
	}
	path, err := resolveConfigPath(configPath)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	pcsconfig.SetConfigDir(filepath.Dir(path))
	old := pcsconfig.Config
	cfg := pcsconfig.NewConfig(path)
	pcsconfig.Config = cfg
	if old != nil && old != cfg {
		_ = old.Close()
	}
	if err = cfg.Init(); err != nil {
		return fmt.Errorf("load config %s: %w", path, err)
	}
	if err = os.Setenv(EnvConfigFile, path); err != nil {
		return fmt.Errorf("export config path for transfer workers: %w", err)
	}
	resolver, err := rc.NewResolver(a.CWD)
	if err != nil {
		return err
	}
	a.ConfigFile = path
	a.Resolver = resolver
	a.initialized = true
	return nil
}

func resolveConfigPath(explicit string) (string, error) {
	path := strings.TrimSpace(explicit)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(EnvConfigFile))
	}
	if path == "" {
		dir := strings.TrimSpace(os.Getenv(EnvConfigDir))
		if dir == "" {
			dir = defaultConfigDir()
		}
		path = filepath.Join(dir, defaultConfigName)
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, defaultConfigName)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func defaultConfigDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			return filepath.Join(dir, "pcsclone")
		}
	} else if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "pcsclone")
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), ".pcsclone")
	}
	return ".pcsclone"
}

type globalOptions struct {
	config  string
	args    []string
	help    bool
	version bool
}

func parseGlobalOptions(args []string) (globalOptions, error) {
	var g globalOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config" || arg == "-c":
			if i+1 >= len(args) {
				return g, usageErrorf("missing value for %s", arg)
			}
			g.config = args[i+1]
			i++
		case strings.HasPrefix(arg, "--config="):
			g.config = strings.TrimPrefix(arg, "--config=")
			if g.config == "" {
				return g, usageErrorf("missing value for --config")
			}
		case arg == "--help" || arg == "-h":
			g.help = true
		case arg == "--version" || arg == "-V":
			g.version = true
		case arg == "--":
			g.args = args[i+1:]
			return g, nil
		case strings.HasPrefix(arg, "-") && arg != "-":
			return g, usageErrorf("unknown global option %q (global options must precede the command)", arg)
		default:
			g.args = args[i:]
			return g, nil
		}
	}
	return g, nil
}

// command describes a leaf command or a command group.
type command struct {
	Name        string
	Aliases     []string
	Category    string
	Summary     string
	Usage       string
	Description string
	MinArgs     int
	MaxArgs     int
	Flags       []flagSpec
	Group       []*command
	NoRuntime   bool
	Run         func(*commandContext) error
}

type commandContext struct {
	app   *App
	ctx   context.Context
	cmd   *command
	args  []string
	flags *flagValues
}

func (a *App) runCommand(ctx context.Context, cmd *command, args []string, configPath string) error {
	if cmd.Group != nil {
		return a.runGroup(ctx, cmd, args, configPath)
	}
	return a.runLeaf(ctx, cmd, args, configPath)
}

func (a *App) runGroup(ctx context.Context, group *command, args []string, configPath string) error {
	if len(args) == 0 {
		a.printCommandHelp(group)
		return nil
	}
	switch args[0] {
	case "--help", "-h":
		a.printCommandHelp(group)
		return nil
	case "help":
		if len(args) == 1 {
			a.printCommandHelp(group)
			return nil
		}
		return a.printHelpSubcommand(group, args[1:])
	}
	if strings.HasPrefix(args[0], "-") {
		return usageErrorf("options must follow a subcommand; run 'pcsclone help %s'", group.UsagePath())
	}
	sub := findCommand(group.Group, args[0])
	if sub == nil {
		return usageErrorf("unknown %s subcommand %q; run 'pcsclone help %s'", group.Name, args[0], group.UsagePath())
	}
	return a.runCommand(ctx, sub, args[1:], configPath)
}

func (a *App) runLeaf(ctx context.Context, cmd *command, args []string, configPath string) error {
	positional, flags, err := parseCommandFlags(args, cmd.Flags)
	if errors.Is(err, errHelpRequested) {
		a.printCommandHelp(cmd)
		return nil
	}
	if err != nil {
		return err
	}
	if len(positional) < cmd.MinArgs || (cmd.MaxArgs >= 0 && len(positional) > cmd.MaxArgs) {
		return usageErrorf("usage: %s", cmd.Usage)
	}
	if !cmd.NoRuntime {
		if err = a.ensureRuntime(configPath); err != nil {
			return operationError(err)
		}
	}
	c := &commandContext{app: a, ctx: ctx, cmd: cmd, args: positional, flags: flags}
	return cmd.Run(c)
}

func (a *App) runHelp(args []string) int {
	if len(args) == 0 {
		a.printGlobalHelp()
		return 0
	}
	cmd := findCommand(topLevelCommands(), args[0])
	if cmd == nil {
		return a.report(usageErrorf("unknown command %q", args[0]))
	}
	return a.report(a.printHelpPath(cmd, args[1:]))
}

func (a *App) printHelpPath(cmd *command, rest []string) error {
	for _, name := range rest {
		if cmd.Group == nil {
			return usageErrorf("%q has no subcommands", cmd.UsagePath())
		}
		next := findCommand(cmd.Group, name)
		if next == nil {
			return usageErrorf("unknown subcommand %q for %s", name, cmd.UsagePath())
		}
		cmd = next
	}
	a.printCommandHelp(cmd)
	return nil
}

func (a *App) printHelpSubcommand(group *command, args []string) error {
	if len(args) == 0 {
		a.printCommandHelp(group)
		return nil
	}
	sub := findCommand(group.Group, args[0])
	if sub == nil {
		return usageErrorf("unknown subcommand %q for %s", args[0], group.UsagePath())
	}
	return a.printHelpPath(sub, args[1:])
}

func findCommand(commands []*command, name string) *command {
	for _, cmd := range commands {
		if cmd.Name == name {
			return cmd
		}
		for _, alias := range cmd.Aliases {
			if alias == name {
				return cmd
			}
		}
	}
	return nil
}

// UsagePath returns the command path without the program name, for messages.
func (c *command) UsagePath() string {
	usage := c.Usage
	usage = strings.TrimPrefix(usage, "pcsclone ")
	if i := strings.IndexAny(usage, " [<"); i >= 0 {
		usage = usage[:i]
	}
	return usage
}

func (a *App) printGlobalHelp() {
	fmt.Fprintf(a.Stdout, `pcsclone %s - standalone local/Baidu netdisk file operations

Usage:
  pcsclone [global options] <command> [arguments]

Global options:
  --config FILE, -c FILE   Use FILE instead of the pcsclone configuration
  --help, -h               Show this help
  --version, -V            Show the version

`, a.Version)

	categories := []string{"Core file operations", "Account configuration", "Baidu netdisk"}
	commands := append([]*command(nil), topLevelCommands()...)
	sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	for _, category := range categories {
		fmt.Fprintf(a.Stdout, "%s:\n", category)
		for _, cmd := range commands {
			if cmd.Category == category {
				fmt.Fprintf(a.Stdout, "  %-14s %s\n", cmd.Name, cmd.Summary)
			}
		}
		fmt.Fprintln(a.Stdout)
	}
	fmt.Fprintf(a.Stdout, `Help:
  help [command ...]       Show command help
  completion SHELL         Generate bash, zsh or PowerShell completion
  version                  Show the version

Paths:
  Local:  D:\Backup, .\Backup, \\server\share
  Remote: NAME:/directory, where NAME is configured by 'config create'
  A local path is resolved against the invocation directory.
  There is no implicit account, remote work directory or 'rc' prefix.

`)
}

func (a *App) printCommandHelp(cmd *command) {
	fmt.Fprintf(a.Stdout, "Usage:\n  %s\n\n", cmd.Usage)
	if cmd.Description != "" {
		fmt.Fprintf(a.Stdout, "%s\n\n", strings.TrimSpace(cmd.Description))
	}
	if cmd.Group != nil {
		fmt.Fprintln(a.Stdout, "Subcommands:")
		names := append([]*command(nil), cmd.Group...)
		sort.SliceStable(names, func(i, j int) bool { return names[i].Name < names[j].Name })
		for _, sub := range names {
			fmt.Fprintf(a.Stdout, "  %-14s %s\n", sub.Name, sub.Summary)
		}
		fmt.Fprintf(a.Stdout, "\nRun 'pcsclone help %s <subcommand>' for details.\n", cmd.UsagePath())
		return
	}
	fmt.Fprintln(a.Stdout, "Options:")
	for _, spec := range cmd.Flags {
		fmt.Fprintf(a.Stdout, "  %-34s %s\n", spec.Left(), spec.Usage)
	}
	fmt.Fprintf(a.Stdout, "  %-34s %s\n", "--help, -h", "Show this help")
}
