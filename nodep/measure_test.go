package nodep

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMalformedPingTargetReturnsError(t *testing.T) {
	for _, method := range []string{"get", "head"} {
		if _, err := MeasureDelayWithType(1, "http://127.0.0.1/\n", "", method); err == nil {
			t.Fatalf("%s: malformed target succeeded", method)
		}
	}
}

func TestPingUsesSelectedHTTPMethod(t *testing.T) {
	methods := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods <- r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, method := range []string{"get", "head"} {
		if _, err := MeasureDelayWithType(2, server.URL, "", method); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"get": http.MethodGet, "head": http.MethodHead}[method]
		if got := <-methods; got != want {
			t.Fatalf("got method %s, want %s", got, want)
		}
	}
}

func TestHTTPPingHonorsConfiguredTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	start := time.Now()
	if _, err := MeasureDelayWithType(1, server.URL, "", "get"); err == nil {
		t.Fatal("stalled HTTP target succeeded")
	}
	if elapsed := time.Since(start); elapsed < 800*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("one-second timeout took %v", elapsed)
	}
}

func TestSOCKSHandshakeHonorsCancellationAndTimeout(t *testing.T) {
	for _, method := range []string{"get", "tcp"} {
		t.Run(method, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan struct{})
			closed := make(chan struct{})
			var once sync.Once
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				close(accepted)
				buffer := make([]byte, 32)
				for {
					if _, err := conn.Read(buffer); err != nil {
						once.Do(func() { close(closed) })
						return
					}
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := MeasureDelayWithContext(ctx, 7, "http://example.invalid", "socks5://"+listener.Addr().String(), method)
				done <- err
			}()
			select {
			case <-accepted:
			case <-time.After(2 * time.Second):
				t.Fatal("proxy connection was not attempted")
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled handshake succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("SOCKS handshake ignored cancellation")
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled proxy connection leaked")
			}
		})
	}
}
