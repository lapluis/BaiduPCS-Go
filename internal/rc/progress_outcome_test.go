package rc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestShortTransferPublishesFinalProgress(t *testing.T) {
	for _, failCallback := range []bool{false, true} {
		s, d := endpoints(t)
		write(t, s, "file", "fixture")
		plan, err := BuildPlan(context.Background(), s, d, "copy", Options{})
		if err != nil {
			t.Fatal(err)
		}
		cause := errors.New("final progress fixture")
		var last ProgressSnapshot
		finals := 0
		result, err := Execute(context.Background(), plan, Options{ShowProgress: true, Progress: func(p ProgressSnapshot) {
			last = p
			if p.Final {
				finals++
				if failCallback {
					panic(cause)
				}
			}
		}})
		if failCallback {
			if !errors.Is(err, ErrProgressCallback) || !errors.Is(err, cause) {
				t.Fatalf("final callback error lost: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if finals != 1 || !last.Final || last.Done != 7 || last.Total != 7 || last.Active != 0 || last.Pending != 0 || last.Completed != 1 || result.Copied != 1 {
			t.Fatalf("incorrect final progress: finals=%d last=%+v result=%+v", finals, last, result)
		}
	}
}

func TestInterruptedProgressRetainsTransferredBytes(t *testing.T) {
	for _, kind := range []string{"canceled", "failed"} {
		t.Run(kind, func(t *testing.T) {
			a := Action{Kind: "copy", Path: "large", Source: &Entry{Size: 100}}
			b := Action{Kind: "copy", Path: "queued", Source: &Entry{Size: 100}}
			s := newProgress(&Plan{Actions: []Action{a, b}})
			s.start(a)
			s.update(a.Path, transferProgress{Done: 40, Rate: 10})
			before := s.snapshot(Result{}, 1)
			s.finish(a, false)
			r := Result{Pending: 1}
			if kind == "canceled" {
				r.Canceled = 1
			} else {
				r.Failed = 1
			}
			after := s.snapshot(r, 0)
			if before.Done != 40 || after.Done != before.Done || after.Total != 200 || after.Rate != 0 || len(after.Files) != 0 || after.Completed != 0 {
				t.Fatalf("interruption lost bytes or reported completion: before=%+v after=%+v", before, after)
			}
			if !strings.Contains(after.String(), kind+"=1") || !strings.Contains(after.String(), "pending=1") {
				t.Fatalf("missing outcomes: %s", after.String())
			}
		})
	}
}

func TestProgressPendingExcludesActiveAndSettledFiles(t *testing.T) {
	p := &Plan{}
	for _, name := range []string{"completed", "active", "canceled", "failed", "queued"} {
		p.Actions = append(p.Actions, Action{Kind: "copy", Path: name, Source: &Entry{Size: 100}})
	}
	s := newProgress(p)
	s.start(p.Actions[0])
	s.finish(p.Actions[0], true)
	s.start(p.Actions[1])
	s.update("active", transferProgress{Done: 20, Rate: 10})
	got := s.snapshot(Result{Copied: 1, Canceled: 1, Failed: 1}, 1)
	if got.Done != 120 || !strings.Contains(got.String(), "canceled=1 pending=1") {
		t.Fatalf("incorrect queue progress: %+v (%s)", got, got.String())
	}
	lines := strings.Join(got.Lines(), "\n")
	if !strings.Contains(lines, "canceled 1") || !strings.Contains(lines, "pending 1") {
		t.Fatalf("terminal outcomes missing: %s", lines)
	}
}
