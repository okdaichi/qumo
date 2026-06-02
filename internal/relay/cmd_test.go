package relay

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupTLS tests TLS configuration setup (basic validation only)
func TestSetupTLSInvalidFiles(t *testing.T) {
	_, err := setupTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Error("Expected error for nonexistent certificate files, got nil")
	}
}

// TestSetupTLSEmptyPaths tests error handling for empty paths
func TestSetupTLSEmptyPaths(t *testing.T) {
	_, err := setupTLS("", "")
	if err == nil {
		t.Error("Expected error for empty certificate paths, got nil")
	}
}

// TestSetupTLS_Insecure verifies that INSECURE=true generates a usable self-signed certificate.
func TestSetupTLS_Insecure(t *testing.T) {
	t.Setenv("INSECURE", "true")

	tlsCfg, err := setupTLS("nonexistent.crt", "nonexistent.key")
	require.NoError(t, err)
	require.NotNil(t, tlsCfg)
	assert.Len(t, tlsCfg.Certificates, 1, "expected exactly one certificate")
	assert.Contains(t, tlsCfg.NextProtos, "h3")
	assert.False(t, tlsCfg.InsecureSkipVerify, "server TLS config must not set InsecureSkipVerify")
}

// --- serveComponents tests ---

type mockServer struct {
	listenCalled   chan struct{}
	shutdownCalled chan struct{}
	listenErr      error
}

func newMockServer(listenErr error) *mockServer {
	return &mockServer{listenCalled: make(chan struct{}), shutdownCalled: make(chan struct{}), listenErr: listenErr}
}

func (m *mockServer) ListenAndServe() error {
	close(m.listenCalled)
	if m.listenErr != nil {
		return m.listenErr
	}
	// Block until Shutdown signals us to exit
	<-m.shutdownCalled
	return nil
}

func (m *mockServer) Shutdown(_ context.Context) error {
	// signal the listen goroutine to exit
	select {
	case <-m.shutdownCalled:
		// already closed
	default:
		close(m.shutdownCalled)
	}
	return nil
}

func TestServeComponents_ShutdownOnContextCancel(t *testing.T) {
	relayMock := newMockServer(nil)
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run serveComponents in background
	go func() { _ = serveComponents(ctx, relayMock, httpMock, 1*time.Second) }()

	// wait for both ListenAndServe to have been invoked
	<-relayMock.listenCalled
	<-httpMock.listenCalled

	// cancel context to trigger shutdown
	cancel()

	// verify Shutdown was called on both mocks
	select {
	case <-relayMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay shutdown was not called")
	}

	select {
	case <-httpMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("http shutdown was not called")
	}
}

func TestServeComponents_IgnoresImmediateListenError(t *testing.T) {
	// Relay.ListenAndServe returns an error immediately. serveComponents should
	// still wait for ctx cancellation and call Shutdown on the other server.
	relayMock := newMockServer(fmt.Errorf("listen failed"))
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = serveComponents(ctx, relayMock, httpMock, 1*time.Second) }()

	// relayMock.listenCalled will be closed quickly even though it returned
	<-relayMock.listenCalled
	<-httpMock.listenCalled

	cancel()

	select {
	case <-httpMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("http shutdown was not called after context cancel")
	}
}

// panicServer simulates a server whose ListenAndServe panics.
// serveComponents should recover the panic and return an error.
type panicServer struct{}

func (p *panicServer) ListenAndServe() error          { panic("boom") }
func (p *panicServer) Shutdown(context.Context) error { return nil }

func TestServeComponents_ReturnsErrorOnPanic(t *testing.T) {
	relayPanic := &panicServer{}
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- serveComponents(ctx, relayPanic, httpMock, 1*time.Second) }()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "panic")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("serveComponents did not return after panic")
	}
}

func TestRun_WildcardRequiresAdvertiseAddr(t *testing.T) {
	t.Setenv("RELAY_ADDR", "0.0.0.0:4433")
	t.Setenv("ADVERTISE_ADDR", "")

	err := Run(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ADVERTISE_ADDR is required")
}

func TestRun_InvalidGroupCacheSize(t *testing.T) {
	t.Setenv("RELAY_ADDR", "localhost:4433")
	t.Setenv("ADVERTISE_ADDR", "localhost:4433")
	t.Setenv("GROUP_CACHE_SIZE", "not-a-number")

	err := Run(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GROUP_CACHE_SIZE")
}


