package rc

import (
	"context"
	"sync"
)

// remoteMutex serializes the legacy client without making canceled requests
// wait for an unrelated RPC to release it. Its zero value is ready to use.
type remoteMutex struct {
	once sync.Once
	held chan struct{}
}

func (m *remoteMutex) init() {
	m.once.Do(func() { m.held = make(chan struct{}, 1) })
}

func (m *remoteMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.init()
	select {
	case m.held <- struct{}{}:
		// Cancellation and lock availability can become ready together.
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *remoteMutex) Lock() { _ = m.LockContext(context.Background()) }

func (m *remoteMutex) Unlock() {
	m.init()
	select {
	case <-m.held:
	default:
		panic("unlock of unlocked remote mutex")
	}
}
