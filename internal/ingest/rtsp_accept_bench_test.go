package ingest

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/rtsp"
)

// benchStartServer is the testing.TB variant of startRTSPServer (rtsp_test.go),
// so benchmarks in this package can stand up a live RTSPServer.
func benchStartServer(tb testing.TB) (*RTSPServer, net.Addr) {
	tb.Helper()
	server := NewRTSPServer(RTSPConfig{
		Addr:     "127.0.0.1:0",
		TrackMux: moqt.NewTrackMux(0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe(ctx) }()
	tb.Cleanup(func() {
		cancel()
		_ = server.Shutdown(context.Background())
		<-errCh
	})
	var addr net.Addr
	for i := 0; i < 200; i++ {
		if addr = server.Addr(); addr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == nil {
		tb.Fatal("RTSP server never started listening")
	}
	return server, addr
}

// BenchmarkRTSPConnCycle measures the per-connection accept→handle→teardown
// cost — the accept-loop goroutine path (connWg.Add → go handleConn → Done)
// changed by removing the deferred connWg.Done(). Each iteration opens a fresh
// loopback connection, issues one OPTIONS request, reads the response, and
// closes. Regression/improvement guard for the accept-loop change.
func BenchmarkRTSPConnCycle(b *testing.B) {
	_, addr := benchStartServer(b)
	u, err := url.Parse("rtsp://" + addr.String() + "/bench")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := net.Dial("tcp", addr.String())
		if err != nil {
			b.Fatal(err)
		}
		rc := rtsp.NewConn(c)
		req := &rtsp.Request{
			Method: rtsp.MethodOptions,
			URL:    u,
			Proto:  "RTSP/1.0",
			Header: map[string][]string{},
		}
		req.Header.Set("CSeq", strconv.Itoa(1))
		if err := rc.WriteRequest(req); err != nil {
			b.Fatal(err)
		}
		if _, _, err := rc.ReadResponse(req); err != nil {
			b.Fatal(err)
		}
		_ = c.Close()
	}
}
