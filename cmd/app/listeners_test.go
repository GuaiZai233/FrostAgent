package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func readListener(t *testing.T, address string) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestListenerSetRebindsAndRetainsOldBindingOnFailure(t *testing.T) {
	set := newListenerSet()
	defer set.Close(context.Background())
	first := availableAddress(t)
	second := availableAddress(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ready")) })
	if err := set.Apply(map[string]http.Handler{first: handler}); err != nil {
		t.Fatal(err)
	}
	if got := readListener(t, first); got != "ready" {
		t.Fatalf("first listener response = %q", got)
	}
	occupied, err := net.Listen("tcp", second)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Apply(map[string]http.Handler{second: handler}); err == nil {
		t.Fatal("occupied address was accepted")
	}
	if got := readListener(t, first); got != "ready" {
		t.Fatalf("old listener was lost after failed bind: %q", got)
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	if err := set.Apply(map[string]http.Handler{second: handler}); err != nil {
		t.Fatal(err)
	}
	if got := readListener(t, second); got != "ready" {
		t.Fatalf("new listener response = %q", got)
	}
}
