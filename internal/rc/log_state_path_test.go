package rc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestLegacyLogProtectsTransferState(t *testing.T) {
	for _, kind := range []string{"partial", "upload", "legacy-upload", "new-partial", "new-upload", "linked-partial", "linked-upload", "linked-legacy-upload"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			configDir := filepath.Join(root, "config")
			previousConfig, previousDir := pcsconfig.Config, pcsconfig.GetConfigDir()
			pcsconfig.Config = pcsconfig.NewConfig(filepath.Join(configDir, "pcs.json"))
			pcsconfig.SetConfigDir(configDir)
			defer func() { pcsconfig.Config = previousConfig; pcsconfig.SetConfigDir(previousDir) }()
			stateDir := filepath.Join(root, ".pcsclone-partials")
			if strings.Contains(kind, "upload") {
				stateDir = filepath.Join(configDir, "rc-state")
			}
			if strings.HasSuffix(kind, "legacy-upload") {
				stateDir = configDir
			}
			if err := os.MkdirAll(stateDir, 0700); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(stateDir, "record.json")
			if strings.HasSuffix(kind, "legacy-upload") {
				state = filepath.Join(stateDir, "pcs_uploading.json")
			}
			original := []byte("synthetic resume state\n")
			newLog := strings.HasPrefix(kind, "new-")
			if !newLog {
				if err := os.WriteFile(state, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			logPath := state
			if strings.HasPrefix(kind, "linked-") {
				logPath = filepath.Join(root, "external.log")
				if err := os.Link(state, logPath); err != nil {
					t.Fatal(err)
				}
			}
			src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
			if err := os.WriteFile(src, []byte("fixture source"), 0600); err != nil {
				t.Fatal(err)
			}
			logArg, err := filepath.Rel(root, logPath)
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := runWithIO([]string{"rc", "moveto", "source", "target", "--log-file", logArg}, root, io.Discard, &stderr)
			if code != 1 || !strings.Contains(stderr.String(), "log file overlaps") {
				t.Errorf("state log accepted: code=%d %q", code, stderr.String())
			}
			if newLog {
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Errorf("log created in state directory: %v", err)
				}
			} else if data, err := os.ReadFile(state); err != nil || !bytes.Equal(data, original) {
				t.Errorf("state changed: %q %v", data, err)
			}
			if data, err := os.ReadFile(src); err != nil || string(data) != "fixture source" {
				t.Errorf("source lost: %q %v", data, err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Errorf("operation started before log validation: %v", err)
			}
		})
	}
}

func TestCommandLogPathStateBoundaries(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	previousDir := pcsconfig.GetConfigDir()
	pcsconfig.SetConfigDir(configDir)
	defer pcsconfig.SetConfigDir(previousDir)
	partialDir := filepath.Join(root, ".pcsclone-partials")
	for _, dir := range []string{configDir, partialDir, filepath.Join(configDir, "rc-state")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(partialDir, "record.json")
	if err := os.WriteFile(state, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	type pathCase struct {
		name, path string
		endpoints  []string
		conflict   bool
	}
	cases := []pathCase{
		{"new partial", filepath.Join(partialDir, "new.log"), []string{"source.bin", "target.bin"}, true},
		{"upload with remote endpoints", filepath.Join(configDir, "rc-state", "new.log"), []string{"from:/file", "dest:/file"}, true},
		{"legacy upload with no endpoints", filepath.Join(configDir, "pcs_uploading.json"), nil, true},
		{"normal log", filepath.Join(root, "events.log"), []string{"source.bin", "target.bin"}, false},
		{"partial sibling prefix", filepath.Join(root, ".pcsclone-partials-extra", "events.log"), []string{"source.bin", "target.bin"}, false},
		{"upload sibling prefix", filepath.Join(configDir, "rc-state-extra", "events.log"), nil, false},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, pathCase{"case alias", strings.ToUpper(state), []string{"source.bin"}, true}, pathCase{"directory alias", filepath.Join(root, ".pcsclone-partials.", "record.json"), []string{"source.bin"}, true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCommandLogPath(context.Background(), root, tc.path, filepath.Join(configDir, "pcs.json"), "", tc.endpoints...)
			if (err != nil) != tc.conflict {
				t.Fatalf("conflict=%v: %v", tc.conflict, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateCommandLogPath(ctx, root, filepath.Join(root, "events.log"), "", "", "source.bin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
