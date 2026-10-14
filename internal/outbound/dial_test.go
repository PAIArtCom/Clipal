package outbound

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestResolvedSameFamilyAttemptsShareDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	client, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	var calls int
	conn, err := dialResolved(ctx, []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")}, "443", func(attempt context.Context, addr string) (net.Conn, error) {
		calls++
		if addr == "192.0.2.1:443" {
			<-attempt.Done()
			return nil, attempt.Err()
		}
		return client, nil
	})
	if err != nil || conn != client || calls != 2 {
		_ = client.Close()
		t.Fatalf("first IP exhausted deadline: calls=%d err=%v", calls, err)
	}
	_ = conn.Close()
}

type observedConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *observedConn) Close() error { c.once.Do(func() { close(c.closed) }); return c.Conn.Close() }

func TestResolvedRaceClosesUnusedSuccessfulConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, ap := net.Pipe()
	b, bp := net.Pipe()
	defer func() { _ = ap.Close(); _ = bp.Close() }()
	first := &observedConn{Conn: a, closed: make(chan struct{})}
	second := &observedConn{Conn: b, closed: make(chan struct{})}
	defer func() { _ = first.Close(); _ = second.Close() }()
	fallbackStarted := make(chan struct{})
	conn, err := dialResolved(ctx, []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}, "443", func(_ context.Context, addr string) (net.Conn, error) {
		if addr == "192.0.2.1:443" {
			<-fallbackStarted
			return first, nil
		}
		close(fallbackStarted)
		return second, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loser := first
	if conn == first {
		loser = second
	}
	select {
	case <-loser.closed:
	case <-time.After(time.Second):
		t.Fatal("unused successful connection leaked")
	}
	winner, ok := conn.(*observedConn)
	if !ok {
		t.Fatal("unexpected winner type")
	}
	select {
	case <-winner.closed:
		t.Fatal("returned connection was cancelled")
	default:
	}
	_ = conn.Close()
}

func TestResolvedCancellationDoesNotDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dialResolved(ctx, []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}, "443", func(context.Context, string) (net.Conn, error) {
		t.Error("dial after cancellation")
		return nil, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
