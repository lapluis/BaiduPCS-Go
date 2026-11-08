package expires

import (
	"fmt"
	"sync/atomic"
	"time"
	_ "unsafe" // for go:linkname
)

type (
	Expires interface {
		IsExpires() bool
		GetExpires() time.Time
		SetExpires(e bool)
		fmt.Stringer
	}

	expires struct {
		expiresAt time.Time
		abort     atomic.Bool
	}
)

//go:linkname stripMono time.(*Time).stripMono
func stripMono(t *time.Time)

// StripMono strip monotonic clocks
func StripMono(t *time.Time) {
	stripMono(t)
}

func NewExpires(dur time.Duration) Expires {
	t := time.Now().Add(dur)
	StripMono(&t)
	return &expires{
		expiresAt: t,
	}
}

func NewExpiresAt(at time.Time) Expires {
	StripMono(&at)
	return &expires{
		expiresAt: at,
	}
}

func (ep *expires) GetExpires() time.Time {
	return ep.expiresAt
}

func (ep *expires) SetExpires(e bool) {
	ep.abort.Store(e)
}

func (ep *expires) IsExpires() bool {
	return ep.abort.Load() || time.Now().After(ep.expiresAt)
}

func (ep *expires) String() string {
	return fmt.Sprintf("expires at: %s, abort: %t", ep.expiresAt, ep.abort.Load())
}
