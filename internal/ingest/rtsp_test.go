package ingest

import (
	"context"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/rtsp"
	"github.com/stretchr/testify/require"
)

func TestRTSPServer(t *testing.T) {
	mux := moqt.NewTrackMux(0)
	cfg := RTSPConfig{
		Addr:     "127.0.0.1:0",
		TrackMux: mux,
	}
	server := NewRTSPServer(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()

	// Wait for listener to start
	var lnAddr net.Addr
	require.Eventually(t, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		if server.listener != nil {
			lnAddr = server.listener.Addr()
			return true
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "RTSP server failed to start listening")

	// Create a raw TCP connection to write RTSP requests
	conn, err := net.Dial("tcp", lnAddr.String())
	require.NoError(t, err)
	defer conn.Close()

	rtspConn := rtsp.NewConn(conn)

	t.Run("OPTIONS", func(t *testing.T) {
		req := &rtsp.Request{
			Method: rtsp.MethodOptions,
			URL:    mustParseURL("rtsp://" + lnAddr.String() + "/teststream"),
			Proto:  "RTSP/1.0",
			Header: make(map[string][]string),
		}
		req.Header.Set("CSeq", "1")

		err := rtspConn.WriteRequest(req)
		require.NoError(t, err)

		resp, _, err := rtspConn.ReadResponse(req)
		require.NoError(t, err)
		require.Equal(t, rtsp.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Public"), "ANNOUNCE")
	})

	t.Run("ANNOUNCE", func(t *testing.T) {
		sdpBody := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=No Name\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\na=tool:libavformat 58.76.100\r\nm=video 0 RTP/AVP 96\r\na=rtpmap:96 H264/90000\r\na=fmtp:96 packetization-mode=1; sprop-parameter-sets=Z2QAH6zZQPARab1g,aOvjyyLA; profile-level-id=4D401F\r\na=control:streamid=0\r\n"

		req := &rtsp.Request{
			Method: rtsp.MethodAnnounce,
			URL:    mustParseURL("rtsp://" + lnAddr.String() + "/teststream"),
			Proto:  "RTSP/1.0",
			Header: make(map[string][]string),
			Body:   io.NopCloser(strings.NewReader(sdpBody)),
		}
		req.Header.Set("CSeq", "2")
		req.Header.Set("Content-Type", "application/sdp")
		req.Header.Set("Content-Length", strconv.Itoa(len(sdpBody)))

		err := rtspConn.WriteRequest(req)
		require.NoError(t, err)

		resp, _, err := rtspConn.ReadResponse(req)
		require.NoError(t, err)
		require.Equal(t, rtsp.StatusOK, resp.StatusCode)
	})

	t.Run("SETUP", func(t *testing.T) {
		req := &rtsp.Request{
			Method: rtsp.MethodSetup,
			URL:    mustParseURL("rtsp://" + lnAddr.String() + "/teststream/streamid=0"),
			Proto:  "RTSP/1.0",
			Header: make(map[string][]string),
		}
		req.Header.Set("CSeq", "3")
		req.Header.Set("Transport", "RTP/AVP/TCP;unicast;interleaved=0-1")

		err := rtspConn.WriteRequest(req)
		require.NoError(t, err)

		resp, _, err := rtspConn.ReadResponse(req)
		require.NoError(t, err)
		require.Equal(t, rtsp.StatusOK, resp.StatusCode)
		require.Equal(t, "RTP/AVP/TCP;unicast;interleaved=0-1", resp.Header.Get("Transport"))
		require.NotEmpty(t, resp.Header.Get("Session"))
	})

	t.Run("RECORD", func(t *testing.T) {
		req := &rtsp.Request{
			Method: rtsp.MethodRecord,
			URL:    mustParseURL("rtsp://" + lnAddr.String() + "/teststream"),
			Proto:  "RTSP/1.0",
			Header: make(map[string][]string),
		}
		req.Header.Set("CSeq", "4")
		req.Header.Set("Session", "12345678")

		err := rtspConn.WriteRequest(req)
		require.NoError(t, err)

		resp, _, err := rtspConn.ReadResponse(req)
		require.NoError(t, err)
		require.Equal(t, rtsp.StatusOK, resp.StatusCode)
	})

	t.Run("TEARDOWN", func(t *testing.T) {
		req := &rtsp.Request{
			Method: rtsp.MethodTeardown,
			URL:    mustParseURL("rtsp://" + lnAddr.String() + "/teststream"),
			Proto:  "RTSP/1.0",
			Header: make(map[string][]string),
		}
		req.Header.Set("CSeq", "5")
		req.Header.Set("Session", "12345678")

		err := rtspConn.WriteRequest(req)
		require.NoError(t, err)

		resp, _, err := rtspConn.ReadResponse(req)
		require.NoError(t, err)
		require.Equal(t, rtsp.StatusOK, resp.StatusCode)
	})

	// Shutdown gracefully
	err = server.Shutdown(context.Background())
	require.NoError(t, err)

	// Server should exit with net.ErrClosed or context.Canceled
	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("ListenAndServe didn't return after Shutdown")
	}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
