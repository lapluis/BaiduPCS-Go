package requester

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// dialTCPAddresses staggers attempts so that an unresponsive address cannot
// consume the entire deadline before another address gets a chance.
func dialTCPAddresses(ctx context.Context, dialer *net.Dialer, network, port string, ips []net.IP) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addresses := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip.To16() == nil || network == "tcp4" && ip.To4() == nil || network == "tcp6" && ip.To4() != nil {
			continue
		}
		addresses = append(addresses, ip)
	}
	if len(addresses) == 0 {
		return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("no suitable IP address found")}
	}
	var cancel context.CancelFunc
	if dialer.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, dialer.Timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result)
	start := func(ip net.IP) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			conn, err := dialTCPConnection(ctx, dialer.DialContext, network, net.JoinHostPort(ip.String(), port))
			select {
			case results <- result{conn, err}:
			case <-ctx.Done():
				if conn != nil {
					conn.Close()
				}
			}
		}()
	}
	start(addresses[0])
	next, active := 1, 1
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	var firstErr error
	for active > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-results:
			active--
			if res.conn != nil {
				return res.conn, nil
			}
			if firstErr == nil {
				firstErr = res.err
			}
			if next < len(addresses) {
				start(addresses[next])
				next++
				active++
				timer.Reset(250 * time.Millisecond)
			}
		case <-timer.C:
			if next < len(addresses) {
				start(addresses[next])
				next++
				active++
				timer.Reset(250 * time.Millisecond)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, firstErr
}

func dialTCPConnection(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, address string) (net.Conn, error) {
	// Windows may transiently fail ephemeral port allocation with 10048.
	// Retry only connection setup, before any HTTP request bytes are sent.
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := dial(ctx, network, address)
		if conn != nil || err == nil {
			return conn, err
		}
		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, cancelErr
		}
		if attempt == 4 || !retryableDialError(err) {
			return nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
