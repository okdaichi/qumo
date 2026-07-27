package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
)

// SubResult is the measured outcome of a subscriber group.
type SubResult struct {
	Connected    int
	Receiving    int
	TotalFrames  int64
}

// SubscribeGroup launches N subscriber goroutines that connect to the given
// relay address, subscribe to the given path/track, and hold for the specified
// duration. It returns the number of connected and receiving sessions.
func SubscribeGroup(ctx context.Context, relayAddr, caFile, path, track string, n int, hold time.Duration) (*SubResult, error) {
	pool, err := loadCACertPool(caFile)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	tlsCfg := &tls.Config{
		RootCAs:    pool,
		NextProtos: []string{moqt.NextProtoMOQ},
		MinVersion: tls.VersionTLS13,
	}
	quicCfg := &quic.Config{
		EnableDatagrams:          true,
		MaxIncomingUniStreams:    1 << 20,
		MaxIncomingStreams:       1 << 20,
		KeepAlivePeriod:          5 * time.Second,
		MaxIdleTimeout:           30 * time.Second,
	}
	dialer := &moqt.Dialer{TLSConfig: tlsCfg, QUICConfig: quicCfg}

	var connCount, receivingCount atomic.Int64
	var wg sync.WaitGroup

	// Safety deadline for each subscriber.
	safety := hold + 60*time.Second

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			subCtx, subCancel := context.WithTimeout(ctx, safety)
			defer subCancel()

			// Retry dial with exponential backoff to handle relay handshake
			// capacity under burst load. Without retry, many simultaneous dials
			// fail and the measured connected count is artificially low.
			sess, err := dialWithRetry(subCtx, dialer, relayAddr)
			if err != nil {
				return
			}
			defer sess.CloseWithError(moqt.NoError, "done")

			tr, err := sess.Subscribe(subCtx, moqt.BroadcastPath(path), moqt.TrackName(track), nil)
			if err != nil {
				return
			}
			defer tr.Close()
			connCount.Add(1)

			buf := moqt.NewFrame(1500)
			gotFrame := false
			for {
				gr, err := tr.AcceptGroup(subCtx)
				if err != nil {
					break
				}
				for range gr.Frames(buf) {
					if !gotFrame {
						gotFrame = true
						receivingCount.Add(1)
					}
				}
			}
		}()
	}

	// Wait for connection count to settle, then hold.
	settleTimeout := 30 * time.Second
	settleFor(ctx, &connCount, n, settleTimeout)

	select {
	case <-ctx.Done():
	case <-time.After(hold):
	}

	// Wait for all subscribers to finish.
	drainWG(&wg, 20*time.Second)

	return &SubResult{
		Connected:   int(connCount.Load()),
		Receiving:   int(receivingCount.Load()),
	}, nil
}

// settleFor waits until connCount reaches want or the deadline elapses.
func settleFor(ctx context.Context, connCount *atomic.Int64, want int, deadline time.Duration) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(deadline)
	for {
		if int(connCount.Load()) >= want {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			return
		case <-tick.C:
		}
	}
}

// drainWG waits for a WaitGroup with a timeout.
func drainWG(wg *sync.WaitGroup, timeout time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// PublishTrack starts a publisher goroutine that trickle-publishes to the hub.
// Returns a cancel function to stop publishing.
func PublishTrack(ctx context.Context, relayAddr, caFile, path, track string, gps float64, size int) (context.CancelFunc, error) {
	pool, err := loadCACertPool(caFile)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	interval := time.Duration(float64(time.Second) / gps)

	tlsCfg := &tls.Config{
		RootCAs:    pool,
		NextProtos: []string{moqt.NextProtoMOQ},
		MinVersion: tls.VersionTLS13,
	}
	quicCfg := &quic.Config{
		EnableDatagrams:          true,
		MaxIncomingUniStreams:    1 << 20,
		MaxIncomingStreams:       1 << 20,
		KeepAlivePeriod:          5 * time.Second,
		MaxIdleTimeout:           30 * time.Second,
	}
	dialer := &moqt.Dialer{TLSConfig: tlsCfg, QUICConfig: quicCfg}

	mux := moqt.NewTrackMux(moqt.NewHopID())
	mux.PublishFunc(ctx, moqt.BroadcastPath(path), func(tw *moqt.TrackWriter) {
		defer tw.Close()
		payload := make([]byte, size)
		if len(payload) < 16 {
			payload = make([]byte, 16)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
				gw, err := tw.OpenGroup(ctx)
				if err != nil || gw == nil {
					continue
				}
				fr := moqt.NewFrame(len(payload))
				_, _ = fr.Write(payload)
				_ = gw.WriteFrame(fr)
				_ = gw.Close()
			}
		}
	})

	go func() {
		sess, err := dialer.Dial(ctx, "moqt://"+relayAddr, mux)
		if err != nil {
			slog.Warn("publisher dial failed", "relay", relayAddr, "err", err)
			return
		}
		<-ctx.Done()
		sess.CloseWithError(moqt.NoError, "done")
	}()

	return cancel, nil
}

// dialWithRetry dials the relay with exponential backoff + jitter so that a burst
// of simultaneous subscriber connections does not trigger a thundering-herd of
// synchronized re-dials that overwhelms the relay's handshake capacity.
func dialWithRetry(ctx context.Context, d *moqt.Dialer, relayAddr string) (*moqt.Session, error) {
	baseDelay := 500 * time.Millisecond
	maxDelay := 15 * time.Second
	attempt := 0

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sess, err := d.Dial(ctx, "moqt://"+relayAddr, moqt.NewTrackMux(0))
		if err == nil {
			return sess, nil
		}
		attempt++
		// Exponential backoff: base * 2^(attempt-1), capped at maxDelay.
		delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt-1)))
		if delay > maxDelay {
			delay = maxDelay
		}
		// Add jitter: random fraction in [0.5, 1.5) to spread retries across
		// goroutines and prevent synchronized thundering-herd re-dials.
		jitter := time.Duration(float64(delay) * (0.5 + rand.Float64()))
		delay = jitter
		slog.Debug("subscriber dial failed, retrying",
			"relay", relayAddr, "attempt", attempt, "delay", delay, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// loadCACertPool reads a PEM cert file into a x509.CertPool.
func loadCACertPool(caFile string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("no certificates found in %q", caFile)
	}
	return pool, nil
}
