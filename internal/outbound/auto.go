package outbound

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

// Automatic routing tries DoH before the OS hostname path (including VPN/TUN).
// All retries happen while establishing a connection,
// so HTTP headers and business request bodies are never replayed.
func connectAutomatically(ctx context.Context, address string, dial DialContext, r *resolver, handshake func(context.Context, net.Conn) (net.Conn, error)) (net.Conn, error) {
	connect := func(ctx context.Context, address string) (net.Conn, error) {
		conn, err := dial(ctx, "tcp", address)
		if err == nil && handshake != nil {
			conn, err = handshake(ctx, conn)
		}
		if err != nil && conn != nil {
			_ = conn.Close()
		}
		return conn, err
	}
	budget := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline) * 3 / 4; remaining < budget {
			budget = remaining
		}
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dohCtx, cancel := context.WithTimeout(ctx, budget)
	ips, dohErr := r.lookup(dohCtx, host)
	// An exhausted full DNS lookup budget is a route failure, rather than caller
	// cancellation. Briefly suppress it on subsequent new connections. Short
	// caller deadlines never establish this backoff.
	if dohErr != nil && dohCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil && budget == 5*time.Second {
		r.rememberFailure(host, dohErr)
	}
	if dohErr == nil {
		var conn net.Conn
		conn, dohErr = dialResolved(dohCtx, ips, port, connect)
		if dohErr == nil {
			cancel()
			return conn, nil
		}
	}
	cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Custom DialTLSContext bypasses net/http's own TLSHandshakeTimeout.
	// Bound the complete OS connection as well, including an otherwise stalled
	// TLS handshake when the caller intentionally has no overall HTTP timeout.
	systemCtx, cancelSystem := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSystem()
	conn, err := connect(systemCtx, address)
	if err != nil {
		return nil, fmt.Errorf("automatic connection failed: DoH: %v; system hostname: %w", dohErr, err)
	}
	return conn, nil
}

func handshakeTLS(ctx context.Context, raw net.Conn, host string, config *tls.Config) (net.Conn, error) {
	if config == nil {
		config = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	copy := config.Clone()
	copy.ServerName = host
	secure := tls.Client(raw, copy)
	if err := secure.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return secure, nil
}
