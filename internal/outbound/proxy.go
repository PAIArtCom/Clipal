package outbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

type forwardDialer struct{ dial DialContext }

func (d forwardDialer) Dial(network, addr string) (net.Conn, error) {
	return d.dial(context.Background(), network, addr)
}
func (d forwardDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dial(ctx, network, addr)
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

func dialRoute(ctx context.Context, target string, p *url.URL, dial DialContext, roots *x509.CertPool) (net.Conn, error) {
	if p == nil {
		return dial(ctx, "tcp", target)
	}
	port := p.Port()
	if port == "" {
		switch p.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	addr := net.JoinHostPort(p.Hostname(), port)
	if p.Scheme == "socks5" || p.Scheme == "socks5h" {
		var auth *proxy.Auth
		if p.User != nil {
			password, _ := p.User.Password()
			auth = &proxy.Auth{User: p.User.Username(), Password: password}
		}
		d, err := proxy.SOCKS5("tcp", addr, auth, forwardDialer{dial})
		if err != nil {
			return nil, fmt.Errorf("invalid SOCKS route")
		}
		contextDialer, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS route lacks cancellation support")
		}
		return contextDialer.DialContext(ctx, "tcp", target)
	}
	if p.Scheme != "http" && p.Scheme != "https" {
		return nil, fmt.Errorf("unsupported account proxy")
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("account proxy connection failed")
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	cancelConn := conn
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// A successful dial may race with cancellation of its attempt context.
			// Once returned, the connection belongs to the caller, not this watcher.
			select {
			case <-done:
				return
			default:
				_ = cancelConn.Close()
			}
		case <-done:
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("account proxy deadline failed")
	}
	if p.Scheme == "https" {
		secure := tls.Client(conn, &tls.Config{ServerName: p.Hostname(), RootCAs: roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("account proxy TLS failed")
		}
		conn = secure
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if p.User != nil {
		password, _ := p.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+password)))
	}
	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("account proxy CONNECT failed")
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("account proxy CONNECT rejected")
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("account proxy deadline reset failed")
	}
	success = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}
