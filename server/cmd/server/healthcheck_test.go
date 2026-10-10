package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthcheckURL(t *testing.T) {
	cases := []struct{ listen, want string }{
		{":8443", "https://127.0.0.1:8443/healthz"},
		{"0.0.0.0:9000", "https://127.0.0.1:9000/healthz"},
		{"[::]:8443", "https://127.0.0.1:8443/healthz"},
		{"10.0.0.5:8443", "https://10.0.0.5:8443/healthz"},
		{"[::1]:8443", "https://[::1]:8443/healthz"},
		{"localhost:8443", "https://localhost:8443/healthz"},
	}
	for _, c := range cases {
		got, err := healthcheckURL(c.listen)
		if err != nil || got != c.want {
			t.Errorf("healthcheckURL(%q) = %q, %v; want %q", c.listen, got, err, c.want)
		}
	}
	for _, bad := range []string{"8443", "10.0.0.5", "0.0.0.0:"} {
		if got, err := healthcheckURL(bad); err == nil {
			t.Errorf("healthcheckURL(%q) = %q, want error", bad, got)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	ok := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	defer ok.Close()
	if err := checkHealth(ok.URL+"/healthz", time.Second); err != nil {
		t.Errorf("healthy server: %v", err)
	}

	failing := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := checkHealth(failing.URL+"/healthz", time.Second); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("503 server: err = %v, want HTTP 503", err)
	}

	gone := httptest.NewTLSServer(http.NotFoundHandler())
	url := gone.URL + "/healthz"
	gone.Close()
	if err := checkHealth(url, time.Second); err == nil {
		t.Error("closed server: want error")
	}
}

func TestHealthcheckCommandUsesHTTPAddr(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	t.Setenv("HTTP_ADDR", ":"+port)
	if code := runHealthcheckCommand(&stderr); code != 0 {
		t.Errorf("exit code = %d (stderr %q), want 0", code, stderr.String())
	}

	stderr.Reset()
	t.Setenv("HTTP_ADDR", "no-port")
	if code := runHealthcheckCommand(&stderr); code != 1 || !strings.Contains(stderr.String(), "HTTP_ADDR") {
		t.Errorf("bad HTTP_ADDR: code = %d, stderr %q; want 1 and an HTTP_ADDR message", code, stderr.String())
	}
}
