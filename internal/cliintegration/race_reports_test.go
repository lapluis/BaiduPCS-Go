package cliintegration

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func raceReportDir(t *testing.T) string {
	t.Helper()
	// Exercise spaces and an apostrophe in runtime flag quoting too.
	dir := filepath.Join(t.TempDir(), "race reports'")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func raceReportOptions(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(dir, "child"))
	quote := "'"
	if strings.Contains(path, quote) {
		quote = `"`
	}
	if strings.Contains(path, quote) {
		t.Fatal("race report path contains both quote characters")
	}
	return strings.TrimSpace(os.Getenv("GORACE") + " log_path=" + quote + path + quote)
}

func raceReports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var reports []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "child.") {
			reports = append(reports, filepath.Join(dir, entry.Name()))
		}
	}
	return reports, nil
}

func childRaceEnv(t *testing.T, env []string) []string {
	t.Helper()
	if !cliRaceEnabled {
		return env
	}
	dir := raceReportDir(t)
	t.Cleanup(func() {
		reports, err := raceReports(dir)
		if err != nil {
			t.Error(err)
		}
		for _, name := range reports {
			file, err := os.Open(name)
			if err != nil {
				t.Errorf("child race report %s: %v", name, err)
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(file, 64<<10))
			_ = file.Close()
			t.Errorf("CLI/worker race report %s: %v\n%s", name, readErr, data)
		}
	})
	return append(env, "GORACE="+raceReportOptions(t, dir))
}

func TestCLIWorkerRaceReportsAreCollected(t *testing.T) {
	if !cliRaceEnabled {
		t.Skip("requires a race-instrumented test executable")
	}
	for _, intentionalRace := range []bool{false, true} {
		t.Run(fmt.Sprint(intentionalRace), func(t *testing.T) {
			dir := raceReportDir(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIWorkerRaceReportFixture$")
			cmd.Env = append(os.Environ(), fmt.Sprintf("PCS_TEST_RACE_REPORT=%t", intentionalRace), "GORACE="+raceReportOptions(t, dir))
			// Simulate the production worker's discarded stderr. The report
			// must remain observable even when an exit is an expected failure.
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			runErr := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			reports, err := raceReports(dir)
			if err != nil {
				t.Fatal(err)
			}
			if intentionalRace {
				if runErr == nil || len(reports) == 0 {
					t.Fatalf("intentional child race lost: err=%v reports=%d", runErr, len(reports))
				}
				data, err := os.ReadFile(reports[0])
				if err != nil || !strings.Contains(string(data), "WARNING: DATA RACE") {
					t.Fatalf("report did not contain race evidence: %v", err)
				}
			} else if runErr != nil || len(reports) != 0 {
				t.Fatalf("healthy child failed: err=%v reports=%d", runErr, len(reports))
			}
		})
	}
}

func TestCLIWorkerRaceReportFixture(t *testing.T) {
	if os.Getenv("PCS_TEST_RACE_REPORT") != "true" {
		return
	}
	var counter int
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer group.Done()
			<-start
			for j := 0; j < 1000; j++ {
				counter++
			}
		}()
	}
	close(start)
	group.Wait()
}
