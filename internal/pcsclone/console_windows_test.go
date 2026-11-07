package pcsclone

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CTRL_C_EVENT cannot be restricted to a process group. Run its sender in a
// fresh console, and check every console member before broadcasting the event.
// The go test process and the user's console never receive this event.
func TestWindowsConsoleInterruptDuringTransferPlanning(t *testing.T) {
	for _, operation := range []string{"download", "upload-move"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsConsoleInterruptController$")
			cmd.Env = append(os.Environ(), "PCS_CONSOLE_INTERRUPT_CONTROLLER="+operation)
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated console test failed: %v\n%s", err, output)
			}
		})
	}
}

func TestWindowsConsoleInterruptController(t *testing.T) {
	operation := os.Getenv("PCS_CONSOLE_INTERRUPT_CONTROLLER")
	if operation == "" {
		return
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var startOnce, cancelOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Consume a POST body so net/http can observe the client's disconnect
		// while this handler waits for cancellation.
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read metadata request: %v", err)
			return
		}
		if r.URL.Query().Get("method") != "meta" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
			cancelOnce.Do(func() { close(canceled) })
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	root := t.TempDir()
	config := filepath.Join(root, "pcs_config.json")
	data := `{"appid":1,"pcs_addr":"pcs.baidu.com","enable_https":false,"proxy":"` + server.URL + `","baidu_active_uid":7,"baidu_user_list":[{"uid":7}]}`
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rc_remotes.json"), []byte(`{"bd":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, "precious.bin")
	want := []byte("preserve the source or existing destination")
	if err := os.WriteFile(local, want, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(local, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsConsoleInterruptCLI$")
	cmd.Env = append(os.Environ(), "PCS_CONSOLE_INTERRUPT_CLI="+operation, "PCS_CONSOLE_INTERRUPT_ROOT="+root)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-done
		}
	}()
	select {
	case <-started:
	case err := <-done:
		joined = true
		t.Fatalf("command exited before metadata request: %v %s", err, output.String())
	case <-ctx.Done():
		t.Fatal("command did not reach local metadata fixture")
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	var pids [32]uint32
	n, _, err := proc.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 2 {
		t.Fatalf("refusing console event: expected exactly controller and CLI, got %d (%v)", n, err)
	}
	members := map[uint32]bool{uint32(os.Getpid()): true, uint32(cmd.Process.Pid): true}
	for _, pid := range pids[:n] {
		if !members[pid] {
			t.Fatalf("refusing console event: unexpected console member %d", pid)
		}
		delete(members, pid)
	}
	if len(members) != 0 {
		t.Fatal("refusing console event: missing expected console member")
	}
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
		t.Fatal(err)
	}
	err = <-done
	joined = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 || !strings.Contains(output.String(), "pcsclone: canceled") {
		t.Fatalf("Ctrl-C did not produce graceful cancellation: %v %s", err, output.String())
	}
	select {
	case <-interrupt:
	case <-time.After(time.Second):
		t.Fatal("controller did not receive its console event")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("metadata HTTP request was not canceled")
	}
	if data, err := os.ReadFile(local); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("local file changed during planning cancellation: %q %v", data, err)
	}
	if info, err := os.Stat(local); err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("local modification time changed: %v %v", info, err)
	}
}

func TestWindowsConsoleInterruptCLI(t *testing.T) {
	operation := os.Getenv("PCS_CONSOLE_INTERRUPT_CLI")
	if operation == "" {
		return
	}
	root := os.Getenv("PCS_CONSOLE_INTERRUPT_ROOT")
	local := filepath.Join(root, "precious.bin")
	args := []string{"--config", filepath.Join(root, "pcs_config.json"), "copyto", "bd:/fixture", local, "-P"}
	if operation == "upload-move" {
		args = []string{"--config", filepath.Join(root, "pcs_config.json"), "moveto", local, "bd:/fixture", "-P"}
	}
	os.Exit(Run(args, "console-test", os.Stdout, os.Stderr))
}
