package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunBootstrap_ListensAndServes(t *testing.T) {
	// Find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		// RunBootstrap blocks; we run it with our chosen port.
		errCh <- RunBootstrap([]string{"--listen", addr, "--ttl", "5s"})
	}()

	// Wait for server to start.
	var connected bool
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			connected = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !connected {
		t.Fatal("server did not start in time")
	}

	client := &http.Client{Timeout: 2 * time.Second}

	// POST /register
	body := `{"id":"test-node","addr":"0.0.0.0:443","region":"us-east"}`
	resp, err := client.Post(
		fmt.Sprintf("http://%s/register", addr),
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /register, got %d", resp.StatusCode)
	}

	// GET /peers
	resp, err = client.Get(fmt.Sprintf("http://%s/peers", addr))
	if err != nil {
		t.Fatalf("peers request failed: %v", err)
	}
	defer resp.Body.Close()

	var peers []struct {
		ID     string `json:"id"`
		Addr   string `json:"addr"`
		Region string `json:"region"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		t.Fatalf("failed to decode peers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].ID != "test-node" {
		t.Errorf("expected id test-node, got %s", peers[0].ID)
	}

	// Shutdown (cancel would normally come from signal, but we use context cancel
	// in tests; RunBootstrap uses signal.NotifyContext, so we send interrupt).
	cancel()

	// RunBootstrap may not exit immediately since we can't send os.Interrupt
	// to ourselves in a unit test. This is tested via subprocess in main_test.go.
	_ = ctx
}
