package cliintegration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
	"golang.org/x/sys/windows"
)

type consoleJobProcessList struct {
	Assigned, Count uint32
	IDs             [32]uintptr
}

func consoleJobPIDs(job windows.Handle) (map[uint32]bool, error) {
	var p consoleJobProcessList
	// The generated wrapper accepts uintptr, so pin the output across its
	// call boundary instead of allowing a stack move to invalidate the address.
	var pin runtime.Pinner
	pin.Pin(&p)
	defer pin.Unpin()
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&p)), uint32(unsafe.Sizeof(p)), nil); err != nil {
		return nil, err
	}
	if p.Count > 32 {
		return nil, fmt.Errorf("too many owned processes: %d", p.Count)
	}
	result := map[uint32]bool{}
	for _, id := range p.IDs[:p.Count] {
		result[uint32(id)] = true
	}
	return result, nil
}

// Run the actual executable and its real transfer worker in an isolated hidden
// console. Never broadcast Ctrl-C into the go test or user's console.
func TestWindowsConsoleDownloadInterruptAndResume(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pcsclone.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/pcsclone")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, mode := range []string{"plain", "shared-partial", "remote-changed"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsConsoleDownloadProcessFixture$")
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
			cmd.Env = append(os.Environ(), "PCS_CONSOLE_DOWNLOAD_BINARY="+binary, "PCS_CONSOLE_DOWNLOAD_ROOT="+t.TempDir(), "PCS_CONSOLE_DOWNLOAD_MODE="+mode)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated download cancellation: %v\n%s", err, output)
			}
		})
	}
}

func TestWindowsConsoleDownloadProcessFixture(t *testing.T) {
	binary := os.Getenv("PCS_CONSOLE_DOWNLOAD_BINARY")
	if binary == "" {
		return
	}
	if err := runConsoleDownloadInterrupt(binary, os.Getenv("PCS_CONSOLE_DOWNLOAD_ROOT"), os.Getenv("PCS_CONSOLE_DOWNLOAD_MODE")); err != nil {
		t.Fatal(err)
	}
}
func runConsoleDownloadInterrupt(binary, root, mode string) error {
	sharedPartial := mode == "shared-partial"
	remoteChanged := mode == "remote-changed"
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	var limitsPin runtime.Pinner
	limitsPin.Pin(&limits)
	defer limitsPin.Unpin()
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	content := make([]byte, 8*1024*1024+17)
	for i := range content {
		content[i] = byte(i*31 + i/251)
	}
	replacement := make([]byte, len(content))
	for i := range content {
		replacement[i] = content[i] ^ 0x5a
	}
	var changed atomic.Bool
	var slow atomic.Bool
	slow.Store(true)
	var served atomic.Int64
	ready := make(chan struct{})
	var readyOnce sync.Once
	gate := make(chan struct{})
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(gate) }) }
	defer release()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Query().Get("method") == "meta" {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			fmt.Fprintf(w, `{"error_code":0,"list":[{"fs_id":7,"path":"/fixture","isdir":0,"size":%d,"mtime":1700000000}]}`, len(content))
			return
		}
		if r.URL.Query().Get("method") == "locatedownload" {
			fmt.Fprint(w, `{"error_code":0,"urls":[{"url":"http://cdn.fixture.invalid/content"}]}`)
			return
		}
		if r.URL.Path != "/content" {
			http.Error(w, "unexpected fixture request", 500)
			return
		}
		begin, end := 0, len(content)-1
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			_, _ = fmt.Sscanf(rangeHeader, "bytes=%d-%d", &begin, &end)
		}
		if begin < 0 || begin > end || end >= len(content) {
			http.Error(w, "bad range", 416)
			return
		}
		data, etag := content, `"console-runtime-v1"`
		if changed.Load() {
			data, etag = replacement, `"console-runtime-v2"`
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("X-Bs-File-Size", fmt.Sprint(len(content)))
		w.Header().Set("Content-Length", fmt.Sprint(end-begin+1))
		if rangeHeader != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(content)))
			w.WriteHeader(206)
		}
		if r.Method == "HEAD" {
			return
		}
		for at := begin; at <= end; {
			next := min(at+16384, end+1)
			n, err := w.Write(data[at:next])
			served.Add(int64(n))
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
			at = next
			if begin > 0 && served.Load() >= 1024*1024 {
				readyOnce.Do(func() { close(ready) })
			}
			if slow.Load() && end-begin > 32768 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
	}))
	defer server.Close()
	// If any assertion fails, closing this job kills only the CLI and its workers
	// before httptest waits for handlers. Never leave a blocked metadata handler.
	defer func() { release(); _ = windows.TerminateJobObject(job, 1) }()
	cfg := filepath.Join(root, "pcs_config.json")
	data := `{"appid":1,"pcs_addr":"pcs.baidu.com","enable_https":false,"cache_size":8192,"proxy":"` + server.URL + `","baidu_active_uid":7,"baidu_user_list":[{"uid":7}]}`
	if err = os.WriteFile(cfg, []byte(data), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "rc_remotes.json"), []byte(`{"bd":7}`), 0600); err != nil {
		return err
	}
	target := filepath.Join(root, "target.bin")
	old := []byte("precious existing destination")
	if err = os.WriteFile(target, old, 0600); err != nil {
		return err
	}
	stamp := time.Unix(1700000000, 0)
	if err = os.Chtimes(target, stamp, stamp); err != nil {
		return err
	}
	var out bytes.Buffer
	start := func() (*exec.Cmd, chan error, error) {
		retries := "0"
		if remoteChanged {
			retries = "1"
		}
		cmd := exec.Command(binary, "--config", cfg, "copyto", "bd:/fixture", target, "-P", "--use-json-log", "--transfers", "1", "--download-threads", "1", "--max-connections", "1", "--retries", retries)
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err == nil {
			err = windows.AssignProcessToJobObject(job, handle)
			windows.CloseHandle(handle)
		}
		if err != nil {
			_ = cmd.Process.Kill()
			<-done
			return nil, nil, err
		}
		release()
		return cmd, done, nil
	}
	_, done, err := start()
	if err != nil {
		return err
	}
	select {
	case <-ready:
	case err := <-done:
		return fmt.Errorf("early CLI exit: %v %s", err, out.String())
	case <-time.After(15 * time.Second):
		return fmt.Errorf("download did not become active")
	}
	owned, err := consoleJobPIDs(job)
	if err != nil {
		return err
	}
	if len(owned) < 2 {
		return fmt.Errorf("no active worker: %v", owned)
	}
	var console [32]uint32
	n, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&console[0])), uintptr(len(console)))
	if n < 3 || n > 32 {
		return fmt.Errorf("refuse event: console count %d %v", n, callErr)
	}
	owned[uint32(os.Getpid())] = true
	for _, pid := range console[:n] {
		if !owned[pid] {
			return fmt.Errorf("refuse event: unowned console process %d", pid)
		}
	}
	if err = windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
		return err
	}
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		return fmt.Errorf("CLI failed to stop after Ctrl-C")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
		return fmt.Errorf("wrong interrupt exit: %v %s", err, out.String())
	}
	if !strings.Contains(out.String(), "copied=0 skipped=0 deleted=0 failed=0 canceled=1 pending=0 bytes=0") {
		return fmt.Errorf("wrong canceled summary: %s", out.String())
	}
	owned, err = consoleJobPIDs(job)
	if err != nil || len(owned) != 0 {
		return fmt.Errorf("workers remain: %v %v", owned, err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, old) {
		return fmt.Errorf("existing target changed: %v", err)
	}
	if info, err := os.Stat(target); err != nil || !info.ModTime().Equal(stamp) {
		return fmt.Errorf("existing target timestamp changed: %v", err)
	}
	parts, err := filepath.Glob(filepath.Join(root, ".pcsclone-partials", "*.part"))
	if err != nil || len(parts) != 1 {
		return fmt.Errorf("missing partial: %v %v", parts, err)
	}
	checkpoint := parts[0] + pcsdownload.DownloadSuffix
	if info, err := os.Stat(checkpoint); err != nil || info.Size() == 0 {
		return fmt.Errorf("missing range checkpoint: %v %v", info, err)
	}
	records, err := filepath.Glob(filepath.Join(root, ".pcsclone-partials", "*.json"))
	if err != nil || len(records) != 1 {
		return fmt.Errorf("missing partial identity record: %v %v", records, err)
	}
	var backupData []byte
	var backupInfo os.FileInfo
	backup := filepath.Join(root, "partial-backup.bin")
	if sharedPartial {
		if err := os.Link(parts[0], backup); err != nil {
			return fmt.Errorf("create external partial backup: %w", err)
		}
		backupData, err = os.ReadFile(backup)
		if err != nil {
			return err
		}
		backupInfo, err = os.Stat(backup)
		if err != nil {
			return err
		}
		// Windows may resolve file IDs lazily. Capture this identity before
		// restarting the CLI rather than opening the path after the transfer.
		if !os.SameFile(backupInfo, backupInfo) {
			return fmt.Errorf("cannot capture external partial backup identity")
		}
	}
	fmt.Println("PASS: real Ctrl-C during active download; exit 130; canceled=1 failed=0; old target and checkpoint preserved; zero remaining workers")
	if remoteChanged {
		// Keep the API's file ID, size and mtime unchanged. Only the HTTP
		// entity identifies the replacement contents after interruption.
		changed.Store(true)
	}
	slow.Store(false)
	before := served.Load()
	out.Reset()
	_, done, err = start()
	if err != nil {
		return err
	}
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		return fmt.Errorf("resumed CLI did not finish")
	}
	if err != nil {
		return fmt.Errorf("resume failed: %v %s", err, out.String())
	}
	got, err := os.ReadFile(target)
	want := content
	if remoteChanged {
		want = replacement
	}
	if err != nil || !bytes.Equal(got, want) {
		return fmt.Errorf("resumed content mismatch: %v", err)
	}
	if sharedPartial {
		if got, err := os.ReadFile(backup); err != nil || !bytes.Equal(got, backupData) {
			return fmt.Errorf("external partial backup overwritten: %v", err)
		}
		if info, err := os.Stat(backup); err != nil || !info.ModTime().Equal(backupInfo.ModTime()) || !os.SameFile(info, backupInfo) {
			return fmt.Errorf("external partial backup identity or timestamp changed: %v", err)
		}
		if info, err := os.Stat(target); err != nil || os.SameFile(info, backupInfo) {
			return fmt.Errorf("published target still shares backup identity: %v", err)
		}
	}
	transferred := served.Load() - before
	if !remoteChanged && transferred >= int64(len(content)) {
		return fmt.Errorf("checkpoint was not reused: %d >= %d", transferred, len(content))
	}
	if remoteChanged && transferred < int64(len(content)) {
		return fmt.Errorf("changed entity did not restart the complete download: %d < %d", transferred, len(content))
	}
	if !strings.Contains(out.String(), fmt.Sprintf("copied=1 skipped=0 deleted=0 failed=0 canceled=0 pending=0 bytes=%d", len(content))) {
		return fmt.Errorf("wrong resumed summary: %s", out.String())
	}
	for _, name := range []string{parts[0], checkpoint, records[0]} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			return fmt.Errorf("published download retained partial state %s: %v", name, err)
		}
	}
	if _, err := os.Stat(records[0] + ".done"); err != nil {
		return fmt.Errorf("completion record missing: %v", err)
	}
	owned, err = consoleJobPIDs(job)
	if err != nil || len(owned) != 0 {
		return fmt.Errorf("workers remain after resume: %v %v", owned, err)
	}
	fmt.Printf("PASS: resumed exact %d bytes; retry served %d bytes; zero remaining workers\n", len(content), transferred)
	return nil
}
