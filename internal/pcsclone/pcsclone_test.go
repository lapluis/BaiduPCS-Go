package pcsclone

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func runCLI(t *testing.T, config string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	full := args
	if config != "" {
		full = append([]string{"--config", config}, args...)
	}
	code := Run(full, "test-version", &stdout, &stderr)
	// Windows cannot remove a directory while the configuration file is open.
	_ = pcsconfig.Config.Close()
	return code, stdout.String(), stderr.String()
}

func mustRun(t *testing.T, config string, args ...string) string {
	t.Helper()
	code, stdout, stderr := runCLI(t, config, args...)
	if code != 0 {
		t.Fatalf("%v exited %d: %s%s", args, code, stdout, stderr)
	}
	return stdout
}

func requireCode(t *testing.T, want int, config string, args ...string) (string, string) {
	t.Helper()
	code, stdout, stderr := runCLI(t, config, args...)
	if code != want {
		t.Fatalf("%v exited %d, want %d: %s%s", args, code, want, stdout, stderr)
	}
	return stdout, stderr
}

func TestParseCommandFlags(t *testing.T) {
	specs := []flagSpec{
		boolFlag("dry-run", "n", ""),
		stringFlag("include", "", "PATTERN", ""),
		intFlag("transfers", "", "N", ""),
		listFlag("exclude", "PATTERN", ""),
	}
	positional, values, err := parseCommandFlags([]string{
		"first", "-n", "--include", "*.txt", "second", "--transfers=3",
		"--exclude", "*.tmp", "--exclude", "*.log", "--", "-literal",
	}, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(positional) != 3 || positional[0] != "first" || positional[1] != "second" || positional[2] != "-literal" {
		t.Fatalf("unexpected positional arguments: %#v", positional)
	}
	if !values.Bool("dry-run") {
		t.Fatal("expected dry-run")
	}
	if values.String("include") != "*.txt" || values.Int("transfers") != 3 {
		t.Fatalf("unexpected values: %#v", values.values)
	}
	if got := values.List("exclude"); len(got) != 2 || got[1] != "*.log" {
		t.Fatalf("unexpected list: %#v", got)
	}

	if _, _, err = parseCommandFlags([]string{"--missing"}, specs); err == nil {
		t.Fatal("unknown option was accepted")
	}
	if _, _, err = parseCommandFlags([]string{"--transfers", "many"}, specs); err == nil {
		t.Fatal("invalid integer was accepted")
	}
	if _, _, err = parseCommandFlags([]string{"--help"}, specs); !errors.Is(err, errHelpRequested) {
		t.Fatalf("expected help sentinel, got %v", err)
	}
	if _, _, err = parseCommandFlags([]string{"--dry-run=false"}, specs); err != nil {
		t.Fatal(err)
	}
}

func TestAliasDerivation(t *testing.T) {
	cases := map[string]string{
		"Alice Smith": "AliceSmith",
		"中文用户":        "u222",
		"_x_":         "u5",
	}
	for name, want := range cases {
		uid := uint64(222)
		if name == "_x_" {
			uid = 5
		}
		got := deriveAlias(name, uid)
		if got != want {
			t.Errorf("deriveAlias(%q) = %q, want %q", name, got, want)
		}
		if !validAlias(got) {
			t.Errorf("derived alias %q is not valid", got)
		}
	}
	used := map[string]bool{"alice": true, "alice-42": true}
	if got := uniqueAlias("alice", used, 42); got != "alice-42-2" {
		t.Fatalf("unexpected unique alias %q", got)
	}
}

func TestResolveConfigPathPrecedence(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.json")
	envFile := filepath.Join(dir, "env-file.json")
	envDir := filepath.Join(dir, "env-dir")

	t.Setenv(EnvConfigFile, envFile)
	t.Setenv(EnvConfigDir, envDir)

	path, err := resolveConfigPath(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if path != explicit {
		t.Fatalf("explicit path lost: %q", path)
	}
	path, err = resolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if path != envFile {
		t.Fatalf("env file lost: %q", path)
	}
	t.Setenv(EnvConfigFile, "")
	path, err = resolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(envDir, defaultConfigName) {
		t.Fatalf("env dir lost: %q", path)
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "powershell"} {
		script, err := completionScript(shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, want := range []string{"pcsclone", "baidu", "config", "--help"} {
			if !strings.Contains(script, want) {
				t.Fatalf("%s completion missing %q", shell, want)
			}
		}
		if !strings.Contains(script, "baidu share") && shell != "powershell" {
			// The generated case table uses a space-separated path.
			t.Fatalf("%s completion missing nested paths", shell)
		}
	}
	if _, err := completionScript("fish"); err == nil {
		t.Fatal("unsupported shell was accepted")
	}
	config := filepath.Join(t.TempDir(), "pcs_config.json")
	requireCode(t, 0, config, "completion", "bash")
	requireCode(t, 2, config, "completion", "fish")
}

func TestRuntimeExportsConfigForWorker(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config", "pcs_config.json")
	app := &App{CWD: root, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := app.ensureRuntime(config); err != nil {
		t.Fatal(err)
	}
	defer pcsconfig.Config.Close()
	if got := os.Getenv(EnvConfigFile); got != config {
		t.Fatalf("worker config env = %q, want %q", got, config)
	}
	if got := pcsconfig.GetConfigDir(); got != filepath.Dir(config) {
		t.Fatalf("config dir = %q, want %q", got, filepath.Dir(config))
	}
	app.Resolver.Aliases["bd"] = 7
	if err := app.Resolver.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config), "rc_remotes.json")); err != nil {
		t.Fatal("remote aliases were not written beside the configuration:", err)
	}
}

func TestHelpAndExitCodes(t *testing.T) {
	code, out, _ := runCLI(t, "", "help")
	if code != 0 || !strings.Contains(out, "Core file operations") {
		t.Fatalf("help failed: %d %q", code, out)
	}
	code, out, _ = runCLI(t, "", "help", "baidu", "share", "create")
	if code != 0 || !strings.Contains(out, "pcsclone baidu share create") {
		t.Fatalf("nested help failed: %d %q", code, out)
	}
	code, _, errOut := runCLI(t, "", "not-a-command")
	if code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown command: %d %q", code, errOut)
	}
	config := filepath.Join(t.TempDir(), "pcs_config.json")
	requireCode(t, 1, config, "baidu", "link", "bd:/file")
	requireCode(t, 2, config, "baidu", "link", "local-file")
	requireCode(t, 2, config, "config", "create", "x")
	requireCode(t, 2, config, "config", "create", "alice", "--cookies", "BDUSS=abc")
	requireCode(t, 1, config, "config", "delete", "nope")
}

func TestLocalWorkflow(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config", "pcs_config.json")
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")

	mustRun(t, config, "mkdir", filepath.Join(source, "nested"))
	mustRun(t, config, "mkdir", filepath.Join(source, "empty"))
	if err := os.WriteFile(filepath.Join(source, "nested", "hello.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "zero.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skip.tmp"), []byte("skip\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Dry run must not create the destination.
	mustRun(t, config, "copy", source, target, "--exclude", "*.tmp", "--create-empty-src-dirs", "--dry-run")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("dry-run created the destination")
	}

	mustRun(t, config, "copy", source, target, "--exclude", "*.tmp", "--create-empty-src-dirs", "--checksum")
	if _, err := os.Stat(filepath.Join(target, "empty")); err != nil {
		t.Fatal("empty source directory missing:", err)
	}
	if _, err := os.Stat(filepath.Join(target, "skip.tmp")); !os.IsNotExist(err) {
		t.Fatal("excluded file was copied")
	}

	recursive := mustRun(t, config, "ls", target)
	if !strings.Contains(recursive, "nested/hello.txt") {
		t.Fatalf("ls did not recurse by default: %q", recursive)
	}
	oneLevel := mustRun(t, config, "ls", target, "--recursive=false")
	if strings.Contains(oneLevel, "nested/hello.txt") {
		t.Fatalf("ls --recursive=false recursed: %q", oneLevel)
	}
	lsf := mustRun(t, config, "lsf", target)
	if strings.Contains(lsf, "nested/hello.txt") {
		t.Fatalf("lsf recursed by default: %q", lsf)
	}
	lsfRecursive := mustRun(t, config, "lsf", target, "-R")
	if !strings.Contains(lsfRecursive, "nested/hello.txt") {
		t.Fatalf("lsf -R did not recurse: %q", lsfRecursive)
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(mustRun(t, config, "lsjson", target)), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("unexpected lsjson entries: %#v", entries)
	}

	var size map[string]int64
	if err := json.Unmarshal([]byte(mustRun(t, config, "size", target, "--json")), &size); err != nil {
		t.Fatal(err)
	}
	if size["count"] != 2 || size["bytes"] != 6 {
		t.Fatalf("unexpected size: %#v", size)
	}
	if text := mustRun(t, config, "size", target); !strings.Contains(text, "Count: 2") || !strings.Contains(text, "Bytes: 6") {
		t.Fatalf("unexpected text size: %q", text)
	}

	var stat map[string]interface{}
	if err := json.Unmarshal([]byte(mustRun(t, config, "stat", filepath.Join(target, "nested", "hello.txt"), "--hash")), &stat); err != nil {
		t.Fatal(err)
	}
	if stat["Hash"] != "b1946ac92492d2347c6235b4d2611184" || stat["Remote"] != false {
		t.Fatalf("unexpected stat: %#v", stat)
	}

	requireCode(t, 0, config, "check", source, target, "--exclude", "*.tmp")
	if err := os.WriteFile(filepath.Join(target, "extra.txt"), []byte("extra\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requireCode(t, 1, config, "check", source, target, "--exclude", "*.tmp")
	requireCode(t, 1, config, "sync", source, target, "--exclude", "*.tmp", "--max-delete", "0")
	mustRun(t, config, "sync", source, target, "--exclude", "*.tmp", "--max-delete", "1")
	if _, err := os.Stat(filepath.Join(target, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("sync did not delete the extra destination file")
	}

	mustRun(t, config, "moveto", filepath.Join(target, "nested", "hello.txt"), filepath.Join(target, "renamed.txt"))
	if _, err := os.Stat(filepath.Join(target, "nested", "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("moveto retained the source")
	}
	mustRun(t, config, "deletefile", filepath.Join(target, "renamed.txt"))
	mustRun(t, config, "delete", target, "--include", "*.txt")
	mustRun(t, config, "rmdirs", target)
	mustRun(t, config, "rmdir", target)

	moved := filepath.Join(root, "moved")
	mustRun(t, config, "move", source, moved, "--delete-empty-src-dirs")
	if _, err := os.Stat(filepath.Join(moved, "nested", "hello.txt")); err != nil {
		t.Fatal("move result missing:", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "empty")); !os.IsNotExist(err) {
		t.Fatal("move --create-empty-src-dirs default unexpectedly copied empty dirs")
	}
	mustRun(t, config, "purge", moved)

	requireCode(t, 1, config, "copy", source, source)
	requireCode(t, 2, config, "copy", source, target, "--transfers", "0")
	requireCode(t, 2, config, "purge", source, "--exclude", "*.txt")
	requireCode(t, 2, config, "tree", source, "--depth", "-2")
}

func TestImportLegacyConfigAndRedaction(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, defaultConfigName)
	legacyJSON := `{
  "baidu_active_uid": 111,
  "baidu_user_list": [
    {"uid": 111, "name": "alice", "bduss": "SECRET-BDUSS-111", "stoken": "SECRET-STOKEN", "cookies": "", "workdir": "/old"},
    {"uid": 222, "name": "用户乙", "bduss": "SECRET-BDUSS-222", "stoken": "", "cookies": "BDUSS=SECRET-COOKIE; STOKEN=x;", "workdir": "/"},
    {"uid": 333, "name": "broken", "bduss": "", "cookies": ""}
  ]
}`
	if err := os.WriteFile(legacyPath, []byte(legacyJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "rc_remotes.json"), []byte(`{"bd": 111, "cjk": 222}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	config := filepath.Join(root, "new", "pcs_config.json")
	var summary map[string]interface{}
	out := mustRun(t, config, "config", "import-pcsgo", "--from", legacyPath, "--json")
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["imported"].(float64) != 2 || summary["skipped"].(float64) != 1 || summary["aliases"].(float64) != 2 {
		t.Fatalf("unexpected import summary: %#v", summary)
	}

	remotes := mustRun(t, config, "listremotes")
	for _, name := range []string{"bd:", "cjk:"} {
		if !strings.Contains(remotes, name) {
			t.Fatalf("missing %s in %q", name, remotes)
		}
	}
	show := mustRun(t, config, "config", "show", "--json")
	if strings.Contains(show, "SECRET") {
		t.Fatalf("config show leaked credentials: %s", show)
	}
	var views []remoteSummary
	if err := json.Unmarshal([]byte(show), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || !views[0].BDUSS {
		t.Fatalf("unexpected show output: %#v", views)
	}
	fileOut := mustRun(t, config, "config", "file")
	if !strings.Contains(fileOut, config) || !strings.Contains(fileOut, "rc_remotes.json") {
		t.Fatalf("unexpected config file output: %q", fileOut)
	}

	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("legacy config was modified")
	}

	mustRun(t, config, "config", "rename", "bd", "backup")
	if after := mustRun(t, config, "listremotes"); !strings.Contains(after, "backup:") || strings.Contains(after, "bd:") {
		t.Fatalf("rename failed: %q", after)
	}
	mustRun(t, config, "config", "delete", "backup", "--keep-account")
}

func TestImportDryRunAndSecondRun(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, defaultConfigName)
	if err := os.WriteFile(legacyPath, []byte(`{"baidu_user_list":[{"uid":7,"name":"seven","bduss":"SECRET"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "new", "pcs_config.json")
	mustRun(t, config, "config", "import-pcsgo", "--from", legacyPath, "--dry-run")
	if remotes := mustRun(t, config, "listremotes"); strings.TrimSpace(remotes) != "" {
		t.Fatalf("dry-run added remotes: %q", remotes)
	}
	mustRun(t, config, "config", "import-pcsgo", "--from", legacyPath)
	out := mustRun(t, config, "config", "import-pcsgo", "--from", legacyPath, "--json")
	var summary map[string]interface{}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["imported"].(float64) != 0 || summary["existing"].(float64) != 1 {
		t.Fatalf("second import should not duplicate the account: %#v", summary)
	}
}
