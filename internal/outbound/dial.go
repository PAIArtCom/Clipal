package outbound

import (
	"context"
	"fmt"
	"net"
	"time"
)

const addressFallbackDelay = 250 * time.Millisecond

type dialResult struct {
	conn net.Conn
	err  error
}

// dialResolved races two address families with a stagger, like net.Dialer.
// At most two attempts run concurrently; losing connections are cancelled or
// closed. Multiple same-family candidates share the remaining request budget.
func dialResolved(ctx context.Context, ips []net.IP, port string, dial func(context.Context, string) (net.Conn, error)) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, fmt.Errorf("account DoH returned no addresses")
	}
	primaryV4 := ips[0].To4() != nil
	var primary, fallback []net.IP
	for _, ip := range ips {
		if (ip.To4() != nil) == primaryV4 {
			primary = append(primary, ip)
		} else {
			fallback = append(fallback, ip)
		}
	}
	series := func(ctx context.Context, candidates []net.IP) (net.Conn, error) {
		for i, ip := range candidates {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			attemptCtx := ctx
			cancel := func() {}
			if len(ips) > 1 {
				budget := 5 * time.Second
				if deadline, ok := ctx.Deadline(); ok {
					if share := time.Until(deadline) / time.Duration(len(candidates)-i); share < budget {
						budget = share
					}
				}
				attemptCtx, cancel = context.WithTimeout(ctx, budget)
			}
			conn, err := dial(attemptCtx, net.JoinHostPort(ip.String(), port))
			cancel()
			if err == nil {
				return conn, nil
			}
			if conn != nil {
				_ = conn.Close()
			}
		}
		return nil, fmt.Errorf("account upstream connection failed")
	}
	if len(fallback) == 0 {
		return series(ctx, primary)
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan dialResult)
	start := func(candidates []net.IP) {
		go func() {
			conn, err := series(raceCtx, candidates)
			select {
			case results <- dialResult{conn: conn, err: err}:
			case <-raceCtx.Done():
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}
	start(primary)
	timer := time.NewTimer(addressFallbackDelay)
	defer timer.Stop()
	timerCh := timer.C
	fallbackStarted := false
	pending := 1
	for pending > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timerCh:
			timerCh = nil
			start(fallback)
			fallbackStarted = true
			pending++
		case result := <-results:
			pending--
			if result.err == nil {
				return result.conn, nil
			}
			if !fallbackStarted {
				timer.Stop()
				timerCh = nil
				start(fallback)
				fallbackStarted = true
				pending++
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, fmt.Errorf("account upstream connection failed")
}
