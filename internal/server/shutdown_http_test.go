package server

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// A handler that waits on r.Context() (like the escalation / permission
// long-polls) must not make daemon shutdown fail with a deadline error.
func TestShutdownHTTPCancelsLongPollRequests(t *testing.T) {
	started := make(chan struct{})
	r := chi.NewRouter()
	r.Get("/wait", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		case <-time.After(time.Minute):
			w.WriteHeader(http.StatusOK)
		}
	})
	s := &Server{router: r}
	httpSrv := s.NewHTTPServer("127.0.0.1:0")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go httpSrv.Serve(ln)

	respCh := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/wait")
		if err != nil {
			respCh <- -1
			return
		}
		resp.Body.Close()
		respCh <- resp.StatusCode
	}()
	<-started

	start := time.Now()
	if err := s.ShutdownHTTP(httpSrv, 2*time.Second); err != nil {
		t.Fatalf("ShutdownHTTP returned error: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("ShutdownHTTP took %v; long-poll was not cancelled", d)
	}
	if code := <-respCh; code != http.StatusGatewayTimeout {
		t.Fatalf("long-poll status = %d, want %d", code, http.StatusGatewayTimeout)
	}
}

// When a handler ignores cancellation, ShutdownHTTP closes it forcibly after
// the timeout instead of returning context.DeadlineExceeded.
func TestShutdownHTTPForceClosesAfterTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	r := chi.NewRouter()
	r.Get("/stuck", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})
	s := &Server{router: r}
	httpSrv := s.NewHTTPServer("127.0.0.1:0")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go httpSrv.Serve(ln)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/stuck")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	if err := s.ShutdownHTTP(httpSrv, 200*time.Millisecond); err != nil {
		t.Fatalf("ShutdownHTTP returned error: %v", err)
	}
}
