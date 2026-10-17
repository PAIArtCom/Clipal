package outbound

import (
	"context"
	"net"
	"strings"
	"time"
)

const dnsFailureBackoff = 5 * time.Second
const maxDNSCacheEntries = 256

type lookupCall struct {
	done chan struct{}
	ips  []net.IP
	err  error
	// A cancelled leader cannot decide another request's DNS outcome.
	retry bool
}

// The resolver belongs to one account/route. Both the positive TTL cache and
// short failure backoff share that boundary; no business response is cached.
func (r *resolver) lookup(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		r.mu.Lock()
		if cached := r.cache[host]; time.Now().Before(cached.until) {
			r.mu.Unlock()
			return copyDNSAddresses(cached.ips), cached.err
		}
		if call := r.pending[host]; call != nil {
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-call.done:
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if call.retry {
					continue
				}
				return copyDNSAddresses(call.ips), call.err
			}
		}
		call := &lookupCall{done: make(chan struct{})}
		if r.pending == nil {
			r.pending = map[string]*lookupCall{}
		}
		r.pending[host] = call
		r.mu.Unlock()

		entry, err := r.queryUncached(ctx, host)
		r.mu.Lock()
		call.ips, call.err = entry.ips, err
		call.retry = ctx.Err() != nil
		if err == nil {
			r.storeAnswerLocked(host, entry)
		} else if !call.retry {
			r.storeFailureLocked(host, err)
		}
		delete(r.pending, host)
		close(call.done)
		r.mu.Unlock()
		return copyDNSAddresses(entry.ips), err
	}
}

func (r *resolver) storeAnswerLocked(host string, entry answer) {
	if r.cache == nil {
		r.cache = map[string]answer{}
	}
	if _, exists := r.cache[host]; !exists && len(r.cache) >= maxDNSCacheEntries {
		// Evict the earliest expiry instead of discarding every warm answer.
		var oldest string
		var expiry time.Time
		for name, cached := range r.cache {
			if oldest == "" || cached.until.Before(expiry) {
				oldest, expiry = name, cached.until
			}
		}
		delete(r.cache, oldest)
	}
	r.cache[host] = entry
}

func (r *resolver) storeFailureLocked(host string, err error) {
	// Do not overwrite a successful answer published by a concurrent operation.
	if entry := r.cache[host]; entry.err == nil && time.Now().Before(entry.until) {
		return
	}
	r.storeAnswerLocked(host, answer{err: err, until: time.Now().Add(dnsFailureBackoff)})
}

func (r *resolver) rememberFailure(host string, err error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	r.mu.Lock()
	defer r.mu.Unlock()
	r.storeFailureLocked(host, err)
}

// Results belong to callers, while cache and in-flight answers stay immutable.
func copyDNSAddresses(ips []net.IP) []net.IP {
	if ips == nil {
		return nil
	}
	copy := make([]net.IP, len(ips))
	for i, ip := range ips {
		copy[i] = append(net.IP(nil), ip...)
	}
	return copy
}
