package outbound

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type dnsCacheTransport func(*http.Request) (*http.Response, error)

func (f dnsCacheTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cacheTestResolver(t *testing.T, handle func(*http.Request) error, ttl ...uint32) (*resolver, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	client := &http.Client{Transport: dnsCacheTransport(func(req *http.Request) (*http.Response, error) {
		count.Add(1)
		if handle != nil {
			if err := handle(req); err != nil {
				return nil, err
			}
		}
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		lifetime := uint32(60)
		if len(ttl) > 0 {
			lifetime = ttl[0]
		}
		message := dnsFixtureAnswer(t, data, lifetime)
		wire, err := message.Pack()
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(wire))}, nil
	})}
	return &resolver{client: client, endpoint: "https://dns.example/dns-query", cache: map[string]answer{}, service: "cloudflare"}, count
}

func TestDNSFailureBackoffIsBoundedAndRecovers(t *testing.T) {
	var failed atomic.Bool
	failed.Store(true)
	r, count := cacheTestResolver(t, func(*http.Request) error {
		if failed.Load() {
			return errors.New("fixture DoH unavailable")
		}
		return nil
	})
	var expiry time.Time
	for i := 0; i < 3; i++ {
		if _, err := r.lookup(context.Background(), "example.com"); err == nil {
			t.Fatal("failure appeared successful")
		}
		r.mu.Lock()
		until := r.cache["example.com"].until
		r.mu.Unlock()
		if i == 0 {
			expiry = until
		} else if !until.Equal(expiry) {
			t.Fatal("cache hit extended failure backoff")
		}
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("failed queries=%d, want 1 during backoff", got)
	}
	r.mu.Lock()
	entry := r.cache["example.com"]
	remaining := time.Until(entry.until)
	if remaining <= 0 || remaining > 5*time.Second {
		t.Errorf("backoff=%s", remaining)
	}
	entry.until = time.Now().Add(-time.Second)
	r.cache["example.com"] = entry
	r.mu.Unlock()
	failed.Store(false)
	for i := 0; i < 2; i++ {
		if _, err := r.lookup(context.Background(), "example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if got := count.Load(); got != 3 {
		t.Fatalf("recovery/cache queries=%d, want failed+A+AAAA", got)
	}
}

// Done is evaluated when lookup joins an existing call's select. The signal
// makes cancellation and coalescing tests wait for actual enrollment.
type dnsWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *dnsWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
func waitForDNSWaiter(t *testing.T, waiting <-chan struct{}) {
	t.Helper()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("DNS waiter did not join in-flight query")
	}
}

func TestDNSConcurrentColdQueriesAreCoalesced(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ttl    uint32
		failed bool
	}{
		{"positive", 60, false}, {"zero TTL", 0, false}, {"failure", 60, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			entered := make(chan struct{}, 32)
			r, count := cacheTestResolver(t, func(req *http.Request) error {
				entered <- struct{}{}
				select {
				case <-release:
				case <-req.Context().Done():
					return req.Context().Err()
				}
				if tc.failed {
					return errors.New("fixture failure")
				}
				return nil
			}, tc.ttl)
			type result struct {
				ips []net.IP
				err error
			}
			const callers = 16
			results := make(chan result, callers)
			lookup := func(ctx context.Context) { ips, err := r.lookup(ctx, "EXAMPLE.com."); results <- result{ips, err} }
			go lookup(context.Background())
			<-entered
			for i := 1; i < callers; i++ {
				ctx := &dnsWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
				go lookup(ctx)
				waitForDNSWaiter(t, ctx.waiting)
			}
			if count.Load() != 1 {
				t.Fatalf("duplicate query before first response: %d", count.Load())
			}
			unblock()
			for i := 0; i < callers; i++ {
				got := <-results
				if (got.err != nil) != tc.failed {
					t.Fatalf("result=%v", got.err)
				}
				if !tc.failed {
					if got.ips[0].String() != "127.0.0.1" {
						t.Fatalf("another caller modified shared result: %s", got.ips[0])
					}
					got.ips[0][0] = 203
				}
			}
			want := int32(2)
			if tc.failed {
				want = 1
			}
			if count.Load() != want {
				t.Fatalf("cold queries=%d, want %d", count.Load(), want)
			}
			r.mu.Lock()
			remaining := len(r.pending)
			r.mu.Unlock()
			if remaining != 0 {
				t.Fatalf("completed lookup retained %d calls", remaining)
			}
		})
	}
}

func TestDNSWaiterCancellationLeavesLeaderRunning(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	r, count := cacheTestResolver(t, func(req *http.Request) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-req.Context().Done():
			return req.Context().Err()
		}
	})
	leader := make(chan error, 1)
	go func() { _, err := r.lookup(context.Background(), "example.com"); leader <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	waiting := &dnsWaitingContext{Context: ctx, waiting: make(chan struct{})}
	go func() { _, err := r.lookup(waiting, "example.com"); waiter <- err }()
	waitForDNSWaiter(t, waiting.waiting)
	cancel()
	if err := <-waiter; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter=%v", err)
	}
	close(release)
	if err := <-leader; err != nil {
		t.Fatal(err)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("queries=%d, want A/AAAA", got)
	}
}

func TestDNSCancelledLeaderDoesNotPoisonWaitersOrCache(t *testing.T) {
	for _, allCancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("all_cancelled=%t", allCancelled), func(t *testing.T) {
			entered := make(chan struct{})
			var calls atomic.Int32
			r, count := cacheTestResolver(t, func(req *http.Request) error {
				if calls.Add(1) == 1 {
					close(entered)
					<-req.Context().Done()
					return req.Context().Err()
				}
				return nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			leader := make(chan error, 1)
			go func() { _, err := r.lookup(ctx, "example.com"); leader <- err }()
			<-entered
			waiter := make(chan error, 1)
			var waiterContext = context.Background()
			if allCancelled {
				waiterContext = ctx
			}
			waiting := &dnsWaitingContext{Context: waiterContext, waiting: make(chan struct{})}
			go func() { _, err := r.lookup(waiting, "example.com"); waiter <- err }()
			waitForDNSWaiter(t, waiting.waiting)
			cancel()
			if err := <-leader; !errors.Is(err, context.Canceled) {
				t.Fatalf("leader=%v", err)
			}
			err := <-waiter
			if allCancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled waiter=%v", err)
				}
			} else if err != nil {
				t.Fatalf("cancelled leader poisoned waiter: %v", err)
			}
			r.mu.Lock()
			pending, cached := len(r.pending), len(r.cache)
			r.mu.Unlock()
			if pending != 0 {
				t.Fatalf("cancelled call retained %d in-flight entries", pending)
			}
			if allCancelled && cached != 0 {
				t.Fatalf("abandoned lookup retained %d cache entries", cached)
			}
			if _, err := r.lookup(context.Background(), "example.com"); err != nil {
				t.Fatal(err)
			}
			if got := count.Load(); got != 3 {
				t.Fatalf("queries=%d, want cancelled query+A+AAAA", got)
			}
		})
	}
}

func TestDNSCacheScopeAndPositiveExpiry(t *testing.T) {
	r, count := cacheTestResolver(t, nil)
	for _, host := range []string{"example.com", "EXAMPLE.com.", "other.example"} {
		if _, err := r.lookup(context.Background(), host); err != nil {
			t.Fatal(err)
		}
	}
	if got := count.Load(); got != 4 {
		t.Fatalf("host cache queries=%d", got)
	}
	other, otherCount := cacheTestResolver(t, nil)
	if _, err := other.lookup(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	if otherCount.Load() != 2 {
		t.Fatal("separate route shared DNS state")
	}
	r.mu.Lock()
	entry := r.cache["example.com"]
	remaining := time.Until(entry.until)
	if remaining <= 0 || remaining > time.Minute {
		t.Errorf("TTL=%s", remaining)
	}
	entry.until = time.Now().Add(-time.Second)
	r.cache["example.com"] = entry
	r.mu.Unlock()
	if _, err := r.lookup(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 6 {
		t.Fatal("expired answer was reused")
	}
}

func TestDNSBackoffDoesNotAllowExplicitPolicyToDialSystem(t *testing.T) {
	r, count := cacheTestResolver(t, func(*http.Request) error { return errors.New("fixture failure") })
	for _, host := range []string{"example.com", "example.com", "other.example"} {
		if _, err := r.lookup(context.Background(), host); err == nil {
			t.Fatal("failure bypassed")
		}
	}
	if count.Load() != 2 {
		t.Fatalf("failure cache is not per-host: %d", count.Load())
	}
	// An automatic connection may skip cached failed DoH and use the hostname.
	dialed := ""
	conn, err := connectAutomatically(context.Background(), "example.com:443", func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if dialed != "example.com:443" || count.Load() != 2 {
		t.Fatalf("automatic fallback dial=%s queries=%d", dialed, count.Load())
	}
}

func TestDNSPositiveTTLZeroAndUpperBound(t *testing.T) {
	for _, ttl := range []uint32{0, 600} {
		t.Run(fmt.Sprint(ttl), func(t *testing.T) {
			r, count := cacheTestResolver(t, nil, ttl)
			for i := 0; i < 2; i++ {
				if _, err := r.lookup(context.Background(), "example.com"); err != nil {
					t.Fatal(err)
				}
			}
			want := int32(2)
			if ttl == 0 {
				want = 4
			}
			if count.Load() != want {
				t.Fatalf("queries=%d want=%d", count.Load(), want)
			}
			r.mu.Lock()
			remaining := time.Until(r.cache["example.com"].until)
			r.mu.Unlock()
			if remaining > 5*time.Minute {
				t.Fatalf("TTL cap=%s", remaining)
			}
		})
	}
}

func TestDNSFailureCacheCapacityDoesNotFlushWarmEntries(t *testing.T) {
	r, _ := cacheTestResolver(t, func(*http.Request) error { return errors.New("unavailable") })
	for i := 0; i < maxDNSCacheEntries+5; i++ {
		_, _ = r.lookup(context.Background(), fmt.Sprintf("host-%d.example", i))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) != maxDNSCacheEntries {
		t.Fatalf("cache size=%d", len(r.cache))
	}
}

func TestAutomaticDNSFullStageTimeoutBacksOffAndRecovers(t *testing.T) {
	var stalled atomic.Bool
	stalled.Store(true)
	r, count := cacheTestResolver(t, func(req *http.Request) error {
		if stalled.Load() {
			<-req.Context().Done()
			return req.Context().Err()
		}
		return nil
	})
	var addresses []string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	for i := 0; i < 2; i++ {
		conn, err := connectAutomatically(context.Background(), "example.com:443", dial, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
	if count.Load() != 1 || len(addresses) != 2 || addresses[0] != "example.com:443" || addresses[1] != "example.com:443" {
		t.Fatalf("queries=%d addresses=%v", count.Load(), addresses)
	}
	r.mu.Lock()
	entry := r.cache["example.com"]
	entry.until = time.Now().Add(-time.Second)
	r.cache["example.com"] = entry
	r.mu.Unlock()
	stalled.Store(false)
	conn, err := connectAutomatically(context.Background(), "example.com:443", dial, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if count.Load() != 3 || addresses[2] != "127.0.0.1:443" {
		t.Fatalf("DoH did not recover: queries=%d addresses=%v", count.Load(), addresses)
	}
}

func TestAutomaticDNSShortCallerDeadlineDoesNotCreateBackoff(t *testing.T) {
	var stalled atomic.Bool
	stalled.Store(true)
	r, count := cacheTestResolver(t, func(req *http.Request) error {
		if stalled.Load() {
			<-req.Context().Done()
			return req.Context().Err()
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	conn, err := connectAutomatically(ctx, "example.com:443", func(_ context.Context, _, address string) (net.Conn, error) {
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	stalled.Store(false)
	if _, err := r.lookup(context.Background(), "example.com"); err != nil {
		t.Fatalf("short caller poisoned cache: %v", err)
	}
	if count.Load() != 3 {
		t.Fatalf("queries=%d", count.Load())
	}
}

func TestDNSEarlierResponseTTLIsNotExtendedBySlowAAAA(t *testing.T) {
	var calls atomic.Int32
	r, count := cacheTestResolver(t, func(*http.Request) error {
		if calls.Add(1)%2 == 0 {
			time.Sleep(1100 * time.Millisecond)
		}
		return nil
	}, 1)
	if _, err := r.lookup(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	until := r.cache["example.com"].until
	r.mu.Unlock()
	if time.Now().Before(until) {
		t.Fatal("A TTL was extended by the later AAAA query")
	}
	if _, err := r.lookup(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 4 {
		t.Fatalf("expired A result was reused: queries=%d", count.Load())
	}
}

func TestDNSReturnedAddressesCannotMutateCachedAnswer(t *testing.T) {
	r, count := cacheTestResolver(t, nil)
	for i := 0; i < 3; i++ {
		ips, err := r.lookup(context.Background(), "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got := ips[0].String(); got != "127.0.0.1" {
			t.Fatalf("cached IP was changed by a caller: %s", got)
		}
		ips[0][0] = 203
		ips[0] = net.ParseIP("192.0.2.7")
	}
	if count.Load() != 2 {
		t.Fatal("isolation required another DNS query")
	}
}

func TestDNSEarliestExpiryIncludesAAAAAnswer(t *testing.T) {
	r, count := cacheTestResolver(t, nil)
	base := r.client.Transport
	r.client.Transport = dnsCacheTransport(func(req *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var message dnsmessage.Message
		if err := message.Unpack(body); err != nil {
			return nil, err
		}
		question := message.Questions[0]
		if question.Type == dnsmessage.TypeAAAA {
			message.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}}}
		}
		wire, err := message.Pack()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(wire))
		return resp, nil
	})
	ips, err := r.lookup(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || ips[1].String() != "2001:db8::1" || count.Load() != 2 {
		t.Fatalf("addresses=%v queries=%d", ips, count.Load())
	}
	r.mu.Lock()
	remaining := time.Until(r.cache["example.com"].until)
	r.mu.Unlock()
	if remaining <= 0 || remaining > time.Second {
		t.Fatalf("shorter AAAA TTL ignored: %s", remaining)
	}
	// IPv6 backing bytes and the outer mixed-family slice belong to callers.
	ips[1][0] = 0xff
	ips[0] = net.ParseIP("192.0.2.8")
	cached, err := r.lookup(context.Background(), "example.com")
	if err != nil || len(cached) != 2 || cached[0].String() != "127.0.0.1" || cached[1].String() != "2001:db8::1" || count.Load() != 2 {
		t.Fatalf("mixed-family cache was mutated: ips=%v err=%v queries=%d", cached, err, count.Load())
	}
}

func TestDNSCapacityEvictsEarliestExpiryOnly(t *testing.T) {
	r, _ := cacheTestResolver(t, nil)
	for i := 0; i < maxDNSCacheEntries; i++ {
		r.cache[fmt.Sprintf("warm-%d.example", i)] = answer{ips: []net.IP{net.ParseIP("127.0.0.1")}, until: time.Now().Add(time.Minute)}
	}
	expired := r.cache["warm-0.example"]
	expired.until = time.Now().Add(-time.Second)
	r.cache["warm-0.example"] = expired
	if _, err := r.lookup(context.Background(), "new.example"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.cache["warm-0.example"]; exists {
		t.Fatal("expired entry was retained")
	}
	if len(r.cache) != maxDNSCacheEntries {
		t.Fatalf("warm cache was flushed: %d", len(r.cache))
	}
	if _, exists := r.cache["warm-1.example"]; !exists {
		t.Fatal("unexpired answer was discarded")
	}
}

func TestDNSPositiveAnswerSurvivesResolvedTargetConnectionFailure(t *testing.T) {
	r, count := cacheTestResolver(t, nil)
	conn, err := connectAutomatically(context.Background(), "example.com:443", func(_ context.Context, _, address string) (net.Conn, error) {
		if address == "127.0.0.1:443" {
			return nil, errors.New("target connection unavailable")
		}
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	ips, err := r.lookup(context.Background(), "example.com")
	if err != nil || len(ips) != 1 || count.Load() != 2 {
		t.Fatalf("target connection error polluted DNS answer: ips=%v err=%v queries=%d", ips, err, count.Load())
	}
}
