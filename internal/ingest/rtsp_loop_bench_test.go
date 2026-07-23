package ingest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
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

// benchAnnounce builds a valid ANNOUNCE request with an H.264 SDP body under
// the given broadcast path (paths must differ across re-announces on one
// connection so each NewSession succeeds).
func benchAnnounce(tb testing.TB, addr net.Addr, path string, cseq int) *rtsp.Request {
	tb.Helper()
	u, err := url.Parse("rtsp://" + addr.String() + path)
	if err != nil {
		tb.Fatal(err)
	}
	req := &rtsp.Request{
		Method: rtsp.MethodAnnounce,
		URL:    u,
		Proto:  "RTSP/1.0",
		Header: map[string][]string{},
	}
	req.Header.Set("CSeq", strconv.Itoa(cseq))
	req.Header.Set("Content-Type", "application/sdp")
	req.Header.Set("Content-Length", strconv.Itoa(len(testH264SDP)))
	req.Body = io.NopCloser(strings.NewReader(testH264SDP))
	return req
}

// BenchmarkRTSPAnnounceLoop drives handleConn's request loop with several
// ANNOUNCE requests on a single connection — the path changed by the
// defer-in-loop session-lifecycle fix. Each ANNOUNCE establishes a fresh
// ingest Session; the fix closes the previous Session before creating a new
// one rather than stacking deferred closes until the connection ends.
//
// Regression guard: ns/op and allocs/op should not worsen versus base.
func BenchmarkRTSPAnnounceLoop(b *testing.B) {
	_, addr := benchStartServer(b)
	const announcesPerConn = 5
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := net.Dial("tcp", addr.String())
		if err != nil {
			b.Fatal(err)
		}
		rc := rtsp.NewConn(c)
		for j := 0; j < announcesPerConn; j++ {
			req := benchAnnounce(b, addr, fmt.Sprintf("/bench%d", j), j+1)
			if err := rc.WriteRequest(req); err != nil {
				b.Fatal(err)
			}
			if _, _, err := rc.ReadResponse(req); err != nil {
				b.Fatal(err)
			}
		}
		_ = c.Close()
	}
}
