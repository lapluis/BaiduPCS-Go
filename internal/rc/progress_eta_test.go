package rc

import (
	"strings"
	"testing"
	"time"
)

func TestProgressETABounds(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total, done, rate int64
		want              time.Duration
	}{
		{"slow-large-file", 128 << 30, 0, 1, -1},
		{"resumed", 128 << 30, (128 << 30) - 768, 128, 6 * time.Second},
		{"fractional", 10, 7, 2, 1500 * time.Millisecond},
		{"complete", 10, 10, 2, 0},
		{"stalled", 10, 7, 0, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Action{Kind: "copy", Path: "file", Source: &Entry{Size: tc.total}}
			s := newProgress(&Plan{Actions: []Action{a}})
			s.start(a)
			s.update(a.Path, transferProgress{Done: tc.done, Rate: tc.rate})
			p := s.snapshot(Result{}, 1)
			if p.ETA != tc.want || p.Done != tc.done || p.Rate != tc.rate {
				t.Fatalf("snapshot=%+v, want ETA=%v", p, tc.want)
			}
			if tc.want < 0 {
				for _, rendered := range []string{p.String(), p.Lines()[0]} {
					if !strings.Contains(rendered, "ETA --") {
						t.Fatalf("unknown ETA rendered as %q", rendered)
					}
				}
			}
		})
	}
}
