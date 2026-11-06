package pcserror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestErrorChainsPreserveUnderlyingCauses(t *testing.T) {
	for name, newError := range map[string]func() Error{
		"pcs":   func() Error { return NewPCSErrorInfo("fixture") },
		"pan":   func() Error { return NewPanErrorInfo("fixture") },
		"xpan":  func() Error { return NewXPanErrorInfo("fixture") },
		"dlink": func() Error { return NewDlinkErrInfo("fixture") },
	} {
		t.Run(name, func(t *testing.T) {
			for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
				original := &net.OpError{Op: "read", Net: "fixture", Err: cause}
				e := newError()
				e.SetNetError(original)
				wrapped := fmt.Errorf("outer: %w", e)
				var op *net.OpError
				if !errors.Is(wrapped, cause) || !errors.As(wrapped, &op) || op != original {
					t.Fatalf("network cause lost: %v", wrapped)
				}
			}
			original := &json.SyntaxError{Offset: 7}
			e := newError()
			e.SetJSONError(original)
			var syntax *json.SyntaxError
			if !errors.As(e, &syntax) || syntax != original {
				t.Fatalf("JSON cause lost: %v", e)
			}
			remote := newError()
			remote.SetRemoteError()
			if errors.Unwrap(remote) != nil || errors.Is(remote, context.Canceled) {
				t.Fatal("remote error acquired an unrelated local cause")
			}
		})
	}
}
