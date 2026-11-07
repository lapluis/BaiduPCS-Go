package pcsclone

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBandwidthParsingRejectsPositiveRatesThatRoundToUnlimited(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    int64
		invalid bool
	}{
		{"", 0, false}, {"0", 0, false}, {"0K", 0, false},
		{"0.0M", 0, false}, {"0e100", 0, false}, {"0x0p-2000", 0, false},
		{"1", 1, false}, {"1.5", 1, false}, {"0.5K", 512, false},
		{"0.0009765625K", 1, false}, {"1.5M", 1572864, false},
		{"4G", 4294967296, false}, {"4611686018427387904", 1 << 62, false},
		{"0.5", 0, true}, {"1e-400", 0, true}, {"1e-400G", 0, true},
		{"-1e-400", 0, true}, {"0x1p-2000", 0, true},
		{"0.0001K", 0, true}, {"0.0000005M", 0, true}, {"0.0000000005G", 0, true},
		{"0.0009765624999999999K", 0, true}, {"-1", 0, true},
		{"NaN", 0, true}, {"+Inf", 0, true}, {"1e100", 0, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseRate(tc.input)
			if got != tc.want || (err != nil) != tc.invalid {
				t.Fatalf("rate %q parsed as %d err=%v", tc.input, got, err)
			}
			if err != nil && !strings.Contains(err.Error(), "--bwlimit") {
				t.Fatalf("missing flag diagnostic: %v", err)
			}
		})
	}
}

func TestInvalidSubByteBandwidthStopsBeforeCopy(t *testing.T) {
	for _, rate := range []string{"0.5", "1e-400", "-1e-400", "0x1p-2000", "0.0001K", "0.0000005M", "0.0000000005G"} {
		t.Run(rate, func(t *testing.T) {
			root := t.TempDir()
			source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
			if err := os.WriteFile(source, []byte("new source contents"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("old target"), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			app := &App{CWD: root, Version: "fixture", Stdout: &out, Stderr: &stderr}
			code := app.run([]string{"--config", filepath.Join(root, "config", "pcs.json"), "copyto", source, target, "--bwlimit", rate})
			if code != 2 || !strings.Contains(stderr.String(), "--bwlimit") {
				t.Errorf("invalid limit did not stop command: code=%d stderr=%q", code, stderr.String())
			}
			for name, want := range map[string]string{source: "new source contents", target: "old target"} {
				if data, err := os.ReadFile(name); err != nil || string(data) != want {
					t.Errorf("invalid limit changed file: data=%q err=%v", data, err)
				}
			}
		})
	}
}
