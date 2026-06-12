//go:build integration

// Package relay integration test. Tagged `integration` so it stays out of the
// default `go test ./...` unit run (it stands up real QUIC relays); run it with
// `go test -tags=integration ./internal/relay/...`.
package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/require"
)

// freeUDPPort returns an ephemeral UDP port on loopback. There is a small TOCTOU
// window between closing the probe socket and the relay binding it — acceptable
// for a local in-process test.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	c, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// TestPeerDiscovery_EdgeConnectsToHubViaLocalResolver is an in-process integration
// test for the LocalResolver path: a real edge relay discovers a real hub relay
// through a fake Nomad service-catalog endpoint and completes a QUIC/MOQT
// handshake to it. No Docker or Nomad required — this complements the manual
// docker/nomad simulation and would catch regressions in the discover→dial loop
// (e.g. the #93 class, where an edge filtered out all hubs).
func TestPeerDiscovery_EdgeConnectsToHubViaLocalResolver(t *testing.T) {
	certFile, keyFile := createTempCert(t)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)

	quicCfg := &quic.Config{
		EnableDatagrams: true,
		KeepAlivePeriod: 5 * time.Second,
		MaxIdleTimeout:  30 * time.Second,
	}
	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{moqt.NextProtoMOQ},
		MinVersion:   tls.VersionTLS13,
	}
	dialerTLS := &tls.Config{
		NextProtos:         []string{moqt.NextProtoMOQ},
		InsecureSkipVerify: true, //nolint:gosec // test only
		MinVersion:         tls.VersionTLS13,
	}

	// ── Hub relay: a real QUIC listener on an ephemeral loopback port ──
	hubAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	hub := &Server{
		MOQServer: &moqt.Server{Addr: hubAddr, TLSConfig: serverTLS, QUICConfig: quicCfg},
		MOQDialer: &moqt.Dialer{TLSConfig: dialerTLS, QUICConfig: quicCfg},
		Config:    &Config{NodeID: "hub-1", Region: "test", Role: "hub", AdvertiseAddr: hubAddr},
	}
	go func() { _ = hub.ListenAndServe() }()
	t.Cleanup(func() {
		// Shutdown (not Close) so teardown can't deadlock: while the edge still
		// holds the peer session open, Close() blocks forever; Shutdown honours
		// the timeout and force-closes, which lets the edge unwind.
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hub.Shutdown(shutCtx)
	})

	// Wait until the hub is accepting QUIC/MOQT sessions before starting the edge,
	// so the edge's first dial succeeds rather than entering the 5s retry backoff.
	require.Eventually(t, func() bool {
		probe := &moqt.Dialer{TLSConfig: dialerTLS, QUICConfig: quicCfg}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		sess, derr := probe.DialQUIC(ctx, hubAddr, moqt.NewTrackMux(0))
		if derr != nil {
			return false
		}
		_ = sess.CloseWithError(0, "probe")
		return true
	}, 5*time.Second, 100*time.Millisecond, "hub never became reachable")

	// ── Fake Nomad: serves the hub as a `qumo-relay` service tagged role=hub ──
	host, portStr, err := net.SplitHostPort(hubAddr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	nomad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]localService{{
			ID:          "hub-1",
			ServiceName: "qumo-relay",
			Address:     host,
			Port:        port,
			Tags:        []string{"role=hub", "region=test"},
			Datacenter:  "dc1",
		}})
	}))
	t.Cleanup(nomad.Close)

	// ── Edge relay: LocalResolver pointed at the fake Nomad ──
	edge := &Server{
		MOQServer: &moqt.Server{Addr: "127.0.0.1:0", TLSConfig: serverTLS, QUICConfig: quicCfg},
		MOQDialer: &moqt.Dialer{TLSConfig: dialerTLS, QUICConfig: quicCfg},
		Config: &Config{
			NodeID: "edge-1", Region: "test", Role: "edge",
			AdvertiseAddr:         "127.0.0.1:1",
			LocalResolverInterval: 200 * time.Millisecond,
		},
		localResolver: &LocalResolver{
			addr:        nomad.URL,
			serviceName: "qumo-relay",
			interval:    200 * time.Millisecond,
			httpClient:  nomad.Client(),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go edge.ConnectPeers(ctx)

	// ── Assert: the edge discovered the hub via Nomad and completed the dial ──
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(metricPeerDialAttempts.WithLabelValues(hubAddr, "ok")) >= 1
	}, 10*time.Second, 200*time.Millisecond, "edge never completed a QUIC handshake to the discovered hub")

	require.GreaterOrEqual(t, testutil.ToFloat64(metricPeersConnected), 1.0,
		"peers_connected should reflect the maintained edge→hub connection")
}
