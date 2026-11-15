package rc

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

var errSlowDownloadTail = errors.New("sustained slow download tail")

type downloadTailKey struct{}

// One attempt owns this sampler. The worker output reader is joined before
// Execute examines fastPeerCandidate. The first offset may be resumed data.
type downloadTail struct {
	now                 func() time.Time
	isLastAfterFastPeer func() bool
	at, slowSince       time.Time
	done                int64
	fastPeerCandidate   bool
}

func (t *downloadTail) observe(now time.Time, v transferProgress) bool {
	if v.Total <= 0 || v.Done <= 0 || v.Done > v.Total {
		return false
	}
	if t.at.IsZero() || v.Done < t.done {
		t.at, t.done, t.slowSince = now, v.Done, time.Time{}
		return false
	}
	elapsed := now.Sub(t.at)
	delta := v.Done - t.done
	if elapsed >= time.Second && elapsed <= 10*time.Second && delta >= 16<<20 && float64(delta)/elapsed.Seconds() >= 4<<20 {
		t.fastPeerCandidate = true
	}
	if elapsed < 5*time.Second {
		return false
	}
	rate := float64(delta) / elapsed.Seconds()
	t.at, t.done = now, v.Done
	if elapsed > 10*time.Second {
		t.slowSince = time.Time{}
		return false
	}
	// Ignore idle heartbeats (including Retry-After), short tails and bursts.
	if delta == 0 || rate > 512<<10 || v.Total-v.Done < 32<<20 || !t.isLastAfterFastPeer() {
		t.slowSince = time.Time{}
		return false
	}
	if t.slowSince.IsZero() {
		t.slowSince = now
	}
	return now.Sub(t.slowSince) >= 30*time.Second
}

func (s *downloadStage) hasResumableRanges() bool {
	if s.info.ID == "" || s.info.ID == "0" || strings.HasPrefix(s.info.ETag, "W/") ||
		(s.info.ETag == "" && s.info.LastModified == "") {
		return false
	}
	// A single-stream worker discards old ranges. Only interrupt when a
	// ranged worker has already persisted a nonempty checkpoint.
	info, err := os.Lstat(s.payload + pcsdownload.DownloadSuffix)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
