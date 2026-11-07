package rc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerOversizedDiagnosticCannotInjectControlFrame(t *testing.T) {
	calls := 0
	output := &workerOutput{ctx: context.WithValue(context.Background(), progressKey{}, func(transferProgress) { calls++ })}
	output.Write([]byte(strings.Repeat("x", 64*1024+1)))
	output.Write([]byte("@pcsclone {\"Reset\":true,\"CheckpointFailed\":true}\n"))
	if output.reset || output.checkpointFailed || calls != 0 {
		t.Fatalf("tail of a diagnostic became a control frame: reset=%t checkpoint=%t calls=%d", output.reset, output.checkpointFailed, calls)
	}
	output.Write([]byte("@pcsclone {\"Done\":1}\n"))
	if calls != 1 {
		t.Fatal("parser did not recover at the next real line")
	}
}

func TestWorkerRejectsOversizedCompleteProtocolFrame(t *testing.T) {
	calls := 0
	output := &workerOutput{ctx: context.WithValue(context.Background(), progressKey{}, func(transferProgress) { calls++ })}
	output.Write([]byte("@pcsclone {\"ETag\":\"" + strings.Repeat("e", 128*1024) + "\",\"Reset\":true}\n"))
	if output.reset || calls != 0 {
		t.Fatal("oversized complete frame bypassed the line limit")
	}
	if len(output.pending) > 64*1024 || cap(output.pending) > 64*1024 {
		t.Fatalf("parser retained oversized memory: len=%d cap=%d", len(output.pending), cap(output.pending))
	}
}

func TestWorkerFinalProtocolMessageIsNotIgnored(t *testing.T) {
	for _, mode := range []string{"plain", "diagnostic", "checkpoint", "truncated", "prefix", "bad-json", "bad-type", "null", "sender"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerFinalProtocolProcessFixture$")
			cmd.Env = append(os.Environ(), "PCS_FINAL_WORKER_PROTOCOL="+mode)
			err := executeWorkerCommand(ctx, cmd, "download", 7)
			if mode == "plain" || mode == "diagnostic" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if mode == "checkpoint" {
				if !errors.Is(err, ErrCheckpointSave) || errors.Is(err, ErrWorkerProtocol) {
					t.Fatalf("final checkpoint classification: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrWorkerProtocol) {
				t.Fatalf("invalid final protocol returned success or lost classification: %v", err)
			}
		})
	}
}

func TestWorkerFinalProtocolProcessFixture(t *testing.T) {
	mode := os.Getenv("PCS_FINAL_WORKER_PROTOCOL")
	if mode == "" {
		return
	}
	switch mode {
	case "plain":
		fmt.Print(`@pcsclone {"Done":8,"Total":8}`)
	case "diagnostic":
		fmt.Print("ignored final diagnostic")
	case "checkpoint":
		fmt.Print(`@pcsclone {"CheckpointFailed":true}`)
	case "truncated":
		fmt.Print(`@pcsclone {`)
	case "prefix":
		fmt.Print(`@pcscl`)
	case "bad-json":
		fmt.Println(`@pcsclone {broken}`)
	case "bad-type":
		fmt.Println(`@pcsclone {"Done":"wrong type"}`)
	case "null":
		fmt.Println(`@pcsclone null`)
	case "sender":
		fmt.Println(`@pcsclone {"ProtocolFailed":true}`)
	}
	os.Exit(0)
}

func TestWorkerProtocolBoundsAreIndependentOfChunking(t *testing.T) {
	for _, size := range []int{maxWorkerProtocolLine - 1, maxWorkerProtocolLine, maxWorkerProtocolLine + 1} {
		for _, chunk := range []int{1, 1024, 32768, maxWorkerProtocolLine + 8} {
			t.Run(fmt.Sprintf("%d/%d", size, chunk), func(t *testing.T) {
				base := workerFramePrefix + `{"ETag":""}`
				frame := workerFramePrefix + `{"ETag":"` + strings.Repeat("e", size-len(base)) + "\"}\n"
				calls, stops := 0, 0
				output := &workerOutput{ctx: context.WithValue(context.Background(), progressKey{}, func(transferProgress) { calls++ }), stop: func() { stops++ }}
				for len(frame) > 0 {
					n := min(chunk, len(frame))
					output.Write([]byte(frame[:n]))
					frame = frame[n:]
					if len(output.pending) > maxWorkerProtocolLine || cap(output.pending) > maxWorkerProtocolLine {
						t.Fatal("unbounded parser storage")
					}
				}
				output.Write([]byte("@pcsclone {\"CheckpointFailed\":true}\n"))
				output.finish()
				output.finish()
				tooLarge := size > maxWorkerProtocolLine
				if errors.Is(output.protocolErr, ErrWorkerProtocol) != tooLarge || !output.checkpointFailed {
					t.Fatalf("chunk-sensitive classification: %v checkpoint=%t", output.protocolErr, output.checkpointFailed)
				}
				if tooLarge {
					if calls != 0 || stops != 1 {
						t.Fatalf("oversized frame delivered: calls=%d stops=%d", calls, stops)
					}
				} else if calls != 2 || stops != 0 {
					t.Fatalf("valid boundary rejected: calls=%d stops=%d", calls, stops)
				}
			})
		}
	}
}

func TestWorkerReporterAppliesWireLimit(t *testing.T) {
	base, _ := json.Marshal(transferProgress{})
	padding := maxWorkerProtocolLine - len(workerFramePrefix) - len(base)
	for _, mode := range []string{"boundary", "one-over", "escaping", "raw-over", "diagnostic-write-error"} {
		t.Run(mode, func(t *testing.T) {
			v := transferProgress{ETag: strings.Repeat("e", padding)}
			switch mode {
			case "one-over", "diagnostic-write-error":
				v.ETag += "e"
			case "escaping":
				v.ETag = strings.Repeat("<", maxWorkerProtocolLine/4)
			case "raw-over":
				v.ETag = strings.Repeat("e", maxWorkerProtocolLine+1)
			}
			var wire bytes.Buffer
			var target io.Writer = &wire
			cause := errors.New("synthetic diagnostic pipe failure")
			fault := &workerReportFault{failAt: 1, cause: cause}
			if mode == "diagnostic-write-error" {
				target = fault
			}
			cancels := 0
			r := newWorkerReporter(target, func() { cancels++ })
			r.Report(v)
			valid := mode == "boundary"
			if (r.Err() == nil) != valid || errors.Is(r.Err(), ErrWorkerProtocol) == valid {
				t.Fatalf("sender limit classification: %v", r.Err())
			}
			if !valid {
				r.Report(transferProgress{Done: 1})
				if cancels != 1 {
					t.Fatal("size failure did not cancel once")
				}
			} else if cancels != 0 {
				t.Fatal("boundary canceled")
			}
			if mode == "diagnostic-write-error" {
				if !errors.Is(r.Err(), cause) || fault.calls != 1 {
					t.Fatal("protocol diagnostic write error lost")
				}
				return
			}
			if wire.Len() > maxWorkerProtocolLine+1 {
				t.Fatal("oversized line was transmitted")
			}
			output := &workerOutput{ctx: context.Background()}
			output.Write(wire.Bytes())
			output.finish()
			if errors.Is(output.protocolErr, ErrWorkerProtocol) == valid {
				t.Fatalf("sender/receiver disagree: %v", output.protocolErr)
			}
		})
	}
}

func TestWorkerProtocolErrorCancelsAndDrains(t *testing.T) {
	for _, mode := range []string{"ordinary", "checkpoint", "canceled", "canceled-checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			marker := filepath.Join(t.TempDir(), "saved")
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerActiveProtocolProcessFixture$")
			cmd.Env = append(os.Environ(), "PCS_ACTIVE_WORKER_PROTOCOL="+mode, "PCS_ACTIVE_WORKER_MARKER="+marker)
			progressCtx := context.WithValue(ctx, progressKey{}, func(transferProgress) {
				if strings.HasPrefix(mode, "canceled") {
					cancel()
				}
			})
			err := executeWorkerCommand(progressCtx, cmd, "download", 7)
			if !errors.Is(err, ErrWorkerProtocol) || errors.Is(err, context.Canceled) != strings.HasPrefix(mode, "canceled") || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("protocol stop/cancellation classification: %v", err)
			}
			checkpointFails := strings.Contains(mode, "checkpoint")
			if errors.Is(err, ErrCheckpointSave) != checkpointFails {
				t.Fatalf("late checkpoint classification lost: %v", err)
			}
			if cmd.ProcessState == nil {
				t.Fatal("child was not joined")
			}
			if !checkpointFails {
				if got, err := os.ReadFile(marker); err != nil || string(got) != "saved" {
					t.Fatalf("child stopped before checkpoint: %q %v", got, err)
				}
			}
		})
	}
}

func TestWorkerActiveProtocolProcessFixture(t *testing.T) {
	mode := os.Getenv("PCS_ACTIVE_WORKER_PROTOCOL")
	if mode == "" {
		return
	}
	fmt.Print("@pcsclone {\"Done\":1}\n@pcsclone {broken}\n")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() != "cancel" {
			continue
		}
		if strings.Contains(mode, "checkpoint") {
			fmt.Print(`@pcsclone {"CheckpointFailed":true}`)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Getenv("PCS_ACTIVE_WORKER_MARKER"), []byte("saved"), 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(3)
}
