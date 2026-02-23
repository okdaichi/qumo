package sdn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNewClient_RequiresURL(t *testing.T) {
	_, err := NewClient(ClientConfig{RelayName: "relay-a"})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestNewClient_RequiresRelayName(t *testing.T) {
	_, err := NewClient(ClientConfig{URL: "http://localhost:8090"})
	if err == nil {
		t.Fatal("expected error for empty RelayName")
	}
}

func TestNewClient_DefaultHeartbeat(t *testing.T) {
	c, err := NewClient(ClientConfig{URL: "http://localhost:8090", RelayName: "relay-a"})
	if err != nil {
		t.Fatal(err)
	}
	if c.config.HeartbeatInterval != 30*time.Second {
		t.Errorf("expected 30s default, got %v", c.config.HeartbeatInterval)
	}
}

func TestClient_RegisterDeregister(t *testing.T) {
	var mu sync.Mutex
	puts := map[string]int{}
	deletes := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			puts[r.URL.Path]++
		case http.MethodDelete:
			deletes[r.URL.Path]++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewClient(ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	c.Register("/live/stream1")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if puts["/announce/relay-a/live/stream1"] != 1 {
		t.Errorf("expected 1 PUT to /announce/relay-a/live/stream1, got %d", puts["/announce/relay-a/live/stream1"])
	}
	mu.Unlock()

	c.Deregister("/live/stream1")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if deletes["/announce/relay-a/live/stream1"] != 1 {
		t.Errorf("expected 1 DELETE, got %d", deletes["/announce/relay-a/live/stream1"])
	}
	mu.Unlock()
}

func TestClient_Heartbeat(t *testing.T) {
	var mu sync.Mutex
	putCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mu.Lock()
			putCount++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewClient(ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	c.Register("/live/stream1")
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-c.done

	mu.Lock()
	if putCount < 3 {
		t.Errorf("expected at least 3 PUTs (initial + heartbeats), got %d", putCount)
	}
	mu.Unlock()
}

func TestClient_DeregisterAllOnClose(t *testing.T) {
	var mu sync.Mutex
	deletes := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			deletes[r.URL.Path]++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewClient(ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)

	c.Register("/live/stream1")
	c.Register("/live/stream2")
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-c.done

	mu.Lock()
	total := 0
	for _, v := range deletes {
		total += v
	}
	mu.Unlock()

	if total != 2 {
		t.Errorf("expected 2 DELETEs on shutdown, got %d", total)
	}
}

func TestClient_Snapshot(t *testing.T) {
	c, err := NewClient(ClientConfig{
		URL:               "http://localhost:8090",
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	c.mu.Lock()
	c.entries["/a"] = struct{}{}
	c.entries["/b"] = struct{}{}
	c.mu.Unlock()

	snap := c.snapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 entries in snapshot, got %d", len(snap))
	}
}

func TestClient_AnnounceURL(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		URL:       "https://sdn.example.com",
		RelayName: "relay-tokyo-1",
	})

	tests := []struct {
		bp   string
		want string
	}{
		{"/live/stream1", "https://sdn.example.com/announce/relay-tokyo-1/live/stream1"},
		{"live/stream2", "https://sdn.example.com/announce/relay-tokyo-1/live/stream2"},
	}

	for _, tt := range tests {
		got := c.announceURL(tt.bp)
		if got != tt.want {
			t.Errorf("announceURL(%q) = %q, want %q", tt.bp, got, tt.want)
		}
	}
}
