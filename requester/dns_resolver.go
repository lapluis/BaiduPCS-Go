package requester

import (
	"context"
	"fmt"
)

// serialDNSResolver keeps lookup slots held until the actual resolver returns.
// It does not detach work from the caller's cancellation context.
type serialDNSResolver struct {
	resolver interface {
		LookupHost(context.Context, string) ([]string, error)
		LookupAddr(context.Context, string) ([]string, error)
	}
	gate chan struct{}
}

func (r *serialDNSResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return r.lookup(ctx, host, false)
}

func (r *serialDNSResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return r.lookup(ctx, addr, true)
}

func (r *serialDNSResolver) lookup(ctx context.Context, value string, reverse bool) (result []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("dns lookup panic for %q: %v", value, recovered)
		}
	}()
	if reverse {
		result, err = r.resolver.LookupAddr(ctx, value)
	} else {
		result, err = r.resolver.LookupHost(ctx, value)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, err
}
