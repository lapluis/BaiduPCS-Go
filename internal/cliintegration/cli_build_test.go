package cliintegration

import (
	"context"
	"debug/buildinfo"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func cliBuildArgs(command string, args ...string) []string {
	result := []string{command}
	if cliRaceEnabled {
		result = append(result, "-race")
	}
	return append(result, args...)
}

func assertCLIRaceBuild(t *testing.T, binary string, testBinary bool) {
	t.Helper()
	if !cliRaceEnabled {
		return
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatalf("read CLI build information: %v", err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			t.Logf("verified race-instrumented CLI (%s)", info.GoVersion)
			return
		}
	}
	// Older Go test binaries omit build settings. Ask their TestMain to
	// report the compiler-selected race build tag instead of skipping proof.
	if testBinary {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, binary, "__pcs_test_race_enabled").CombinedOutput()
		if err == nil && strings.TrimSpace(string(output)) == "pcsclone-test-race=true" {
			t.Logf("verified race-instrumented test CLI via build-tag probe (%s)", info.GoVersion)
			return
		}
		t.Fatalf("test CLI race probe failed: %v\n%s", err, output)
	}
	t.Fatalf("test host uses -race but child CLI does not: %s", binary)
}
