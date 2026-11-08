package rc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func FuzzWorkerProtocolChunking(f *testing.F) {
	for _, wire := range []string{
		"",
		"@pcsclone {\"Done\":1,\"Total\":2}\n@pcsclone {\"CheckpointFailed\":true}\n",
		"diagnostic\n@pcsclone {\"Done\":1}\r\n",
		"@pcscl",
		"@pcsclone {broken}\n@pcsclone {\"CheckpointFailed\":true}\n",
		"@pcsclone {\"ProtocolFailed\":true}\n@pcsclone {\"Reset\":true}\n",
		strings.Repeat("x", maxWorkerProtocolLine+1) + "@pcsclone {\"Reset\":true}\n@pcsclone {\"Done\":1}\n",
		"@pcsclone {\"ETag\":\"" + strings.Repeat("e", maxWorkerProtocolLine) + "\"}\n@pcsclone {\"CheckpointFailed\":true}",
	} {
		for _, panicOnProgress := range []bool{false, true} {
			f.Add([]byte(wire), uint16(7), panicOnProgress)
		}
	}
	f.Fuzz(func(t *testing.T, wire []byte, split uint16, panicOnProgress bool) {
		if len(wire) > 2*maxWorkerProtocolLine+128 {
			t.Skip()
		}
		type outcome struct {
			events                         []transferProgress
			reset, checkpoint              bool
			protocolErr, callbackErr       string
			protocolFailure, callbackPanic bool
			stops                          int
		}
		cause := errors.New("synthetic progress callback failure")
		read := func(chunk int) outcome {
			var result outcome
			ctx := context.WithValue(context.Background(), progressKey{}, func(v transferProgress) {
				result.events = append(result.events, v)
				if panicOnProgress {
					panic(cause)
				}
			})
			output := &workerOutput{ctx: ctx, stop: func() { result.stops++ }}
			for offset := 0; offset < len(wire); {
				end := min(len(wire), offset+chunk)
				if n, err := output.Write(wire[offset:end]); n != end-offset || err != nil {
					t.Fatalf("drain stopped: n=%d err=%v", n, err)
				}
				if len(output.pending) > maxWorkerProtocolLine || cap(output.pending) > maxWorkerProtocolLine {
					t.Fatal("parser storage exceeded line limit")
				}
				offset = end
			}
			output.finish()
			output.finish() // Finishing an already drained stream must be harmless.
			result.reset, result.checkpoint = output.reset, output.checkpointFailed
			if output.protocolErr != nil {
				result.protocolErr = output.protocolErr.Error()
			}
			if output.callbackErr != nil {
				result.callbackErr = output.callbackErr.Error()
			}
			result.protocolFailure = errors.Is(output.protocolErr, ErrWorkerProtocol)
			result.callbackPanic = errors.Is(output.callbackErr, ErrProgressCallback) && errors.Is(output.callbackErr, cause)
			return result
		}
		whole := read(max(1, len(wire)))
		fragmented := read(int(split) + 1)
		if !reflect.DeepEqual(whole, fragmented) {
			t.Fatalf("chunk-sensitive protocol: bytes=%d chunk=%d events=%d/%d stops=%d/%d protocol=%q/%q callback=%q/%q", len(wire), int(split)+1, len(whole.events), len(fragmented.events), whole.stops, fragmented.stops, whole.protocolErr, fragmented.protocolErr, whole.callbackErr, fragmented.callbackErr)
		}
	})
}
