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
	switch strings.ToLower(strings.TrimSpace(pingType)) {
	case "get":
		return measureHTTPDelay(timeout, target, proxy, http.MethodGet)
	case "tcp":
		return measureTCPDelay(timeout, target, proxy)
	case "head", "":
		return measureHTTPDelay(timeout, target, proxy, http.MethodHead)
	default:
		return PingDelayError, errors.New("unsupported ping type: " + pingType)
	}
}

func measureHTTPDelay(timeout int, target string, proxy string, method string) (int64, error) {
	httpTimeout := time.Second * time.Duration(timeout)
	c, err := CoreHTTPClient(httpTimeout, proxy)
	if err != nil {
		return PingDelayError, err
	}
	delay, err := PingHTTPRequest(c, target, timeout, method)
	if err != nil {
		return delay, err
	}

	return delay, nil
}

func CoreHTTPClient(timeout time.Duration, proxy string) (*http.Client, error) {
	dialContext, err := coreDialContext(timeout, proxy)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		DialContext:           dialContext,
	}

	c := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	return c, nil
}

func PingHTTPRequest(c *http.Client, url string, timeout int, method string) (int64, error) {
	start := time.Now()
	req, _ := http.NewRequest(method, url, nil)
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

func measureTCPDelay(timeout int, target string, proxy string) (int64, error) {
	address, err := resolveTCPTarget(target)
	if err != nil {
		return PingDelayError, err
	}
	deadline := time.Second * time.Duration(timeout)
	baseDialer := &net.Dialer{Timeout: deadline}
	start := time.Now()
	var conn net.Conn
	if len(proxy) > 0 {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return PingDelayError, err
		}
		dialer, err := xproxy.FromURL(proxyURL, baseDialer)
		if err != nil {
			return PingDelayError, err
		}
		conn, err = dialer.Dial("tcp", address)
		if err != nil {
			return mapDelayError(start, timeout, err)
		}
	} else {
		conn, err = baseDialer.Dial("tcp", address)
		if err != nil {
			return mapDelayError(start, timeout, err)
		}
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
	baseDialer := &net.Dialer{Timeout: timeout}
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

	type dialResult struct {
		conn net.Conn
		err  error
	}

	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		resultCh := make(chan dialResult, 1)
		go func() {
			conn, err := dialer.Dial(network, address)
			resultCh <- dialResult{conn: conn, err: err}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-resultCh:
			return result.conn, result.err
		}
	}, nil
}

func mapDelayError(start time.Time, timeout int, err error) (int64, error) {
	delay := time.Since(start).Milliseconds()
	precision := delay - int64(timeout)*1000
	if math.Abs(float64(precision)) < 50 {
		return PingDelayTimeout, err
	}
	return PingDelayError, err
}
