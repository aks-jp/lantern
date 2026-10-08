package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthURL(t *testing.T) {
	tests := map[string]string{
		"":               "http://127.0.0.1:8080/healthz",
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"[::]:9000":      "http://127.0.0.1:9000/healthz",
		"127.0.0.1:8090": "http://127.0.0.1:8090/healthz",
		"[::1]:8090":     "http://[::1]:8090/healthz",
	}
	for in, want := range tests {
		got, err := healthURL(in)
		if err != nil || got != want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := healthURL("8080"); err == nil {
		t.Error("missing port separator must fail")
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ok.Close()
	if code := healthcheck(ok.Listener.Addr().String()); code != 0 {
		t.Errorf("healthy server: exit %d", code)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if code := healthcheck(bad.Listener.Addr().String()); code != 1 {
		t.Errorf("unhealthy server: exit %d", code)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if code := healthcheck(addr); code != 1 {
		t.Errorf("no server: exit %d", code)
	}
}
