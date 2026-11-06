package rc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestCommandLogPathProtectsJunctionStateHardlink(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	target := filepath.Join(root, "state-target")
	for _, dir := range []string{configDir, target} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	junction := filepath.Join(configDir, "rc-state")
	// Directory junctions do not require the symbolic-link privilege. Pass
	// fixture paths through the environment so they are never shell source.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"New-Item -ItemType Junction -Path $env:PCSCLONE_TEST_JUNCTION -Target $env:PCSCLONE_TEST_JUNCTION_TARGET -ErrorAction Stop | Out-Null")
	cmd.Env = append(os.Environ(), "PCSCLONE_TEST_JUNCTION="+junction, "PCSCLONE_TEST_JUNCTION_TARGET="+target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create fixture junction: %v %s", err, output)
	}
	state := filepath.Join(target, "resume.json")
	if err := os.WriteFile(state, []byte("synthetic state"), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "events.log")
	if err := os.Link(state, logPath); err != nil {
		t.Fatal(err)
	}
	previousDir := pcsconfig.GetConfigDir()
	pcsconfig.SetConfigDir(configDir)
	defer pcsconfig.SetConfigDir(previousDir)
	err := ValidateCommandLogPath(context.Background(), root, logPath, filepath.Join(configDir, "pcs.json"), "", "source", "target")
	if err == nil || !strings.Contains(err.Error(), "log file overlaps") {
		t.Fatalf("junction state hardlink accepted: %v", err)
	}
}
