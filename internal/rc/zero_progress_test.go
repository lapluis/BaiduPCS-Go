package rc

import (
	"strings"
	"testing"
)

func TestZeroByteProgressUsesFileOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    ProgressSnapshot
		want string
	}{
		{"queued", ProgressSnapshot{Count: 2, Pending: 2}, "0.0%"},
		{"partly-complete", ProgressSnapshot{Count: 2, Completed: 1, Pending: 1}, "50.0%"},
		{"complete", ProgressSnapshot{Count: 2, Completed: 2, Final: true}, "100.0%"},
		{"failed", ProgressSnapshot{Count: 2, Completed: 1, Failed: 1, Final: true}, "50.0%"},
		{"canceled", ProgressSnapshot{Count: 2, Canceled: 1, Pending: 1, Final: true}, "0.0%"},
		{"initial-empty", ProgressSnapshot{}, "0.0%"},
		{"finished-empty", ProgressSnapshot{Final: true}, "100.0%"},
		{"failed-empty", ProgressSnapshot{Final: true, Failed: 1}, "0.0%"},
		{"byte-progress", ProgressSnapshot{Done: 5, Total: 10, Count: 1, Completed: 1}, "50.0%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rendered := range []string{tc.p.String(), tc.p.Lines()[0]} {
				if !strings.HasPrefix(rendered, tc.want+" |") {
					t.Errorf("incorrect progress: got=%q want prefix=%q", rendered, tc.want)
				}
			}
		})
	}
}
