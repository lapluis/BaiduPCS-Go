package rc

import (
	"math"
	"testing"
	"time"
)

func TestProgressAggregateRateSaturates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rates []int64
		want  int64
	}{
		{"normal", []int64{10, 20, 30}, 60},
		{"exact-limit", []int64{math.MaxInt64 - 1, 1}, math.MaxInt64},
		{"one-byte-over", []int64{math.MaxInt64, 1}, math.MaxInt64},
		{"two-maxima", []int64{math.MaxInt64, math.MaxInt64}, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plan{}
			for i := range tc.rates {
				p.Actions = append(p.Actions, Action{Kind: "copy", Path: string(rune('a' + i)), Source: &Entry{Size: 8}})
			}
			s := newProgress(p)
			for i, a := range p.Actions {
				s.start(a)
				s.update(a.Path, transferProgress{Done: 1, Rate: tc.rates[i]})
			}
			got := s.snapshot(Result{}, len(tc.rates))
			if got.Rate != tc.want {
				t.Fatalf("aggregate rate=%d want=%d", got.Rate, tc.want)
			}
			if got.Done != int64(len(tc.rates)) || got.Total != 8*int64(len(tc.rates)) || len(got.Files) != len(tc.rates) {
				t.Fatal("rate aggregation changed byte accounting", got)
			}
		})
	}
}

func TestProgressAggregateRateExcludesStaleSamples(t *testing.T) {
	a := Action{Kind: "copy", Path: "current", Source: &Entry{Size: 8}}
	b := Action{Kind: "copy", Path: "stale", Source: &Entry{Size: 8}}
	s := newProgress(&Plan{Actions: []Action{a, b}})
	for _, action := range []Action{a, b} {
		s.start(action)
		s.update(action.Path, transferProgress{Done: 1, Rate: math.MaxInt64})
	}
	s.updated[b.Path] = time.Now().Add(-6 * time.Second)
	got := s.snapshot(Result{}, 2)
	if got.Rate != math.MaxInt64 || got.Files[0].Rate != math.MaxInt64 || got.Files[1].Rate != 0 {
		t.Fatal("stale rate included", got)
	}
	if got.Done != 2 || got.Total != 16 {
		t.Fatal("stale sample lost transferred bytes", got)
	}
}
