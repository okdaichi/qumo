package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Register(t *testing.T) {
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		URL:      srv.URL,
		Interval: time.Hour,
	}, "relay-1", "1.2.3.4:4433", "us-east", "edge")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.register(ctx)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if gotBody["id"] != "relay-1" {
		t.Errorf("id = %q, want relay-1", gotBody["id"])
	}
	if gotBody["addr"] != "1.2.3.4:4433" {
		t.Errorf("addr = %q, want 1.2.3.4:4433", gotBody["addr"])
	}
	if gotBody["region"] != "us-east" {
		t.Errorf("region = %q, want us-east", gotBody["region"])
	}
	if gotBody["role"] != "edge" {
		t.Errorf("role = %q, want edge", gotBody["role"])
	}
}

func TestClient_FetchPeers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/peers" {
			if r.URL.Query().Get("self_id") != "" {
				t.Errorf("self_id should not be sent, got %q", r.URL.Query().Get("self_id"))
			}
			if r.URL.Query().Get("region") != "us-east" {
				t.Errorf("region = %q, want us-east", r.URL.Query().Get("region"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"peers": []Node{
				{ID: "relay-2", Addr: "2.2.2.2:4433"},
				{ID: "relay-3", Addr: "3.3.3.3:4433"},
			}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		URL:      srv.URL,
		Interval: time.Hour,
	}, "relay-1", "1.2.3.4:4433", "us-east", "edge")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peers, err := c.FetchPeers(ctx, PeerQuery{PreferredRegion: "us-east"})
	if err != nil {
		t.Fatalf("FetchPeers: %v", err)
	}

	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}
	if peers[0].ID != "relay-2" || peers[1].ID != "relay-3" {
		t.Errorf("unexpected peers: %+v", peers)
	}
}

func TestClient_FetchPeers_FiltersSelf(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/peers" {
			w.Header().Set("Content-Type", "application/json")
			// Include this node's own ID in the response.
			json.NewEncoder(w).Encode(map[string]any{"peers": []Node{
				{ID: "relay-1", Addr: "1.1.1.1:4433"},
				{ID: "relay-2", Addr: "2.2.2.2:4433"},
			}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		URL:      srv.URL,
		Interval: time.Hour,
	}, "relay-1", "1.1.1.1:4433", "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peers, err := c.FetchPeers(ctx, PeerQuery{})
	if err != nil {
		t.Fatalf("FetchPeers: %v", err)
	}

	// Self (relay-1) must be filtered out.
	if len(peers) != 1 {
		t.Fatalf("got %d peers, want 1 (self filtered)", len(peers))
	}
	if peers[0].ID != "relay-2" {
		t.Errorf("expected relay-2, got %s", peers[0].ID)
	}
}

func TestClient_Run(t *testing.T) {
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			callCount++
			w.WriteHeader(http.StatusOK)
			return
		}
		// Run no longer calls /peers; fail if it does.
		t.Errorf("unexpected path: %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		URL:      srv.URL,
		Interval: 50 * time.Millisecond,
	}, "relay-1", "1.1.1.1:4433", "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	c.Run(ctx)

	if callCount < 2 {
		t.Errorf("register called %d times, want >= 2", callCount)
	}
}

func TestClient_RegisterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		URL:      srv.URL,
		Interval: time.Hour,
	}, "relay-1", "1.1.1.1:4433", "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.register(ctx)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
}
