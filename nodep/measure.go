package nodep

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const (
	PingDelayTimeout int64 = 11000
	PingDelayError   int64 = 10000
)

// get the delay of some outbound.
// timeout means how long the http request will be cancelled if no response, in units of seconds.
// url means the website we use to test speed. "https://www.google.com" is a good choice for most cases.
// proxy means the local http/socks5 proxy, like "socks5://[::1]:1080". If proxy is empty, it means no proxy.
func MeasureDelay(timeout int, url string, proxy string) (int64, error) {
	return MeasureDelayWithType(timeout, url, proxy, "head")
}

func MeasureDelayWithType(timeout int, target string, proxy string, pingType string) (int64, error) {
	return MeasureDelayWithContext(context.Background(), timeout, target, proxy, pingType)
}

func MeasureDelayWithContext(ctx context.Context, timeout int, target string, proxy string, pingType string) (int64, error) {
	if timeout <= 0 {
		return PingDelayError, errors.New("ping timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second*time.Duration(timeout))
	defer cancel()
	switch strings.ToLower(strings.TrimSpace(pingType)) {
	case "get":
		return measureHTTPDelay(ctx, timeout, target, proxy, http.MethodGet)
	case "tcp":
		return measureTCPDelay(ctx, timeout, target, proxy)
	case "head", "":
		return measureHTTPDelay(ctx, timeout, target, proxy, http.MethodHead)
	default:
		return PingDelayError, errors.New("unsupported ping type: " + pingType)
	}
}

func measureHTTPDelay(ctx context.Context, timeout int, target string, proxy string, method string) (int64, error) {
	httpTimeout := time.Second * time.Duration(timeout)
	c, err := CoreHTTPClient(ctx, httpTimeout, proxy)
	if err != nil {
		return PingDelayError, err
	}
	delay, err := pingHTTPRequest(ctx, c, target, timeout, method)
	if err != nil {
		return delay, err
	}

	return delay, nil
}

func CoreHTTPClient(ctx context.Context, timeout time.Duration, proxy string) (*http.Client, error) {
	dialContext, err := coreDialContext(timeout, proxy)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		// Transport detaches request cancellation while dialing to allow connection reuse.
		// A ping owns its dial exclusively, so its context must also cancel the handshake.
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			return dialContext(ctx, network, address)
		},
	}

	c := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	return c, nil
}

func pingHTTPRequest(ctx context.Context, c *http.Client, url string, timeout int, method string) (int64, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return PingDelayError, err
	}
	req.Close = true
	req.Header.Set("User-Agent", "yaxc-ping")
	req.Header.Set("Accept", "*/*")
	resp, err := c.Do(req)
	delay := time.Since(start).Milliseconds()
	if err != nil {
		precision := delay - int64(timeout)*1000
		if math.Abs(float64(precision)) < 50 {
			return PingDelayTimeout, err
		} else {
			return PingDelayError, err
		}
	} else if resp != nil {
		resp.Body.Close()
	}
	return delay, nil
}

func measureTCPDelay(ctx context.Context, timeout int, target string, proxy string) (int64, error) {
	address, err := resolveTCPTarget(target)
	if err != nil {
		return PingDelayError, err
	}
	dialContext, err := coreDialContext(time.Second*time.Duration(timeout), proxy)
	if err != nil {
		return PingDelayError, err
	}
	start := time.Now()
	conn, err := dialContext(ctx, "tcp", address)
	if err != nil {
		return mapDelayError(start, timeout, err)
	}
	defer conn.Close()
	return time.Since(start).Milliseconds(), nil
}

func resolveTCPTarget(target string) (string, error) {
	parsed, err := url.Parse(target)
	if err == nil && parsed.Host != "" {
		if parsed.Port() != "" {
			return parsed.Host, nil
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http":
			return net.JoinHostPort(parsed.Hostname(), "80"), nil
		default:
			return net.JoinHostPort(parsed.Hostname(), "443"), nil
		}
	}

	if strings.Contains(target, ":") {
		return target, nil
	}
	return net.JoinHostPort(target, "443"), nil
}

func coreDialContext(timeout time.Duration, proxy string) (func(context.Context, string, string) (net.Conn, error), error) {
	baseDialer := &deadlineDialer{Dialer: net.Dialer{Timeout: timeout}, timeout: timeout}
	if len(proxy) == 0 {
		return baseDialer.DialContext, nil
	}

	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return nil, err
	}
	dialer, err := xproxy.FromURL(proxyURL, baseDialer)
	if err != nil {
		return nil, err
	}

	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, errors.New("proxy dialer does not support cancellation")
	}
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		conn, err := contextDialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if err := conn.SetDeadline(time.Time{}); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}, nil
}

// Bound the SOCKS handshake as well as the TCP connection to the proxy.
type deadlineDialer struct {
	net.Dialer
	timeout time.Duration
}

func (d *deadlineDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *deadlineDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.Dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(d.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func mapDelayError(start time.Time, timeout int, err error) (int64, error) {
	delay := time.Since(start).Milliseconds()
	precision := delay - int64(timeout)*1000
	if math.Abs(float64(precision)) < 50 {
		return PingDelayTimeout, err
	}
	return PingDelayError, err
}
