package rc

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

func TestProgressHeartbeatDoesNotHideWaiting(t *testing.T) {
	for _, done := range []int64{0, 40, 100} {
		a := Action{Kind: "copy", Path: "large.mkv", Source: &Entry{Size: 100}}
		s := newProgress(&Plan{Actions: []Action{a}})
		s.start(a)
		s.update(a.Path, transferProgress{Done: done, Rate: 20})
		s.advanced[a.Path] = time.Now().Add(-6 * time.Second)
		// Periodic reports can keep arriving while the network or finalization
		// is blocked, without advancing any bytes.
		s.update(a.Path, transferProgress{Done: done, Rate: 20})
		p := s.snapshot(Result{}, 1)
		if p.Rate != 0 || p.Done != done || len(p.Files) != 1 || p.Files[0].Rate != 0 || p.Files[0].Idle < 6*time.Second {
			t.Fatalf("heartbeat hid wait: %+v files=%+v", p, p.Files)
		}
		if p.ETA != -1 || !strings.Contains(p.String(), "ETA --") || !strings.Contains(p.String(), "waiting=1") {
			t.Fatalf("incorrect waiting estimate: %s (ETA %v)", p.String(), p.ETA)
		}
		if line := p.Lines()[2]; !strings.Contains(line, "waiting 6s") || !strings.Contains(line, a.Path) {
			t.Fatalf("missing file wait: %q", line)
		}
	}
}

func TestProgressByteChangeEndsWaiting(t *testing.T) {
	for _, done := range []int64{0, 60} {
		a := Action{Kind: "copy", Path: "file", Source: &Entry{Size: 100}}
		s := newProgress(&Plan{Actions: []Action{a}})
		s.start(a)
		s.update(a.Path, transferProgress{Done: 40, Rate: 20})
		s.advanced[a.Path] = time.Now().Add(-time.Minute)
		s.update(a.Path, transferProgress{Done: done, Rate: 10})
		p := s.snapshot(Result{}, 1)
		if p.Rate != 10 || p.Done != done || p.Files[0].Idle >= 5*time.Second || strings.Contains(p.String(), "waiting=") {
			t.Fatalf("byte advance or reset retained wait: %+v files=%+v", p, p.Files)
		}
		s.finish(a, false)
		if len(s.advanced) != 0 || len(s.snapshot(Result{Canceled: 1}, 0).Files) != 0 {
			t.Fatal("finished file retained wait state")
		}
	}
}

func TestWaitingProgressKeepsUnicodePathWithinWidth(t *testing.T) {
	p := ProgressSnapshot{Files: []FileProgress{{Path: strings.Repeat("日本語", 50) + "\x1b[2J", Idle: 123 * time.Second}}}
	line := p.Lines()[2]
	if !strings.Contains(line, "waiting 2m3s") || !strings.Contains(line, "日本語") || strings.Contains(line, "\x1b") || runewidth.StringWidth(line) > 78 {
		t.Fatalf("unsafe or missing waiting progress: %q", line)
	}
}
