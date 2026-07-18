//go:build integration

// STATUS (2026-07-16): UNVERIFIED / blocked on Windows. This probe compiles and
// is hang-proof at the Go level (session-close teardown), but on this Windows box
// it non-deterministically hangs and clients fail to connect (delivered=0) — the
// same quic-go teardown-deadlock class as the relay. Run on Linux to get a clean
// number. Kept as scaffolding for the Linux/CI run.

// Pure-QUIC packets/sec probe — isolates the socket/quic-go ceiling from the
// relay layer. 1 server, K clients, NO relay, NO MoQ. Decides whether the relay's
// ~63K fps core-independent ceiling lives in quic-go/socket (→ GSO is the lever)
// or is relay-specific (→ multiplexing/hierarchy).
//
// PACED independent writers: K server goroutines, each writing a 1200B frame to
// its own client stream TARGET_FPS times/sec (ticker-paced, never unbounded —
// unbounded writes triggered a quic-go teardown deadlock on Windows). Aggregate
// target = TARGET_FPS × K. If delivered tracks the target (well past ~63K), the
// socket is NOT the ceiling → the relay adds it. If delivered caps near ~63K
// regardless of K/target, the socket IS the ceiling.
//
// Teardown is hang-proof: a runCtx bounds the write phase; on expiry we close the
// server SESSIONS (errors every stream Write/Read, unblocking writers/clients)
// and cancel the dial context (stragglers fail fast).

package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"
)

const pureQUICALPN = "purequic-pps"

func BenchmarkPureQUIC_PPS(b *testing.B) {
	cert, pool := chainCert(b)
	targetFPS := envIntDef("TARGET_FPS", 500)
	if targetFPS <= 0 {
		targetFPS = 500
	}
	dur := 6 * time.Second
	if d := os.Getenv("BENCH_DURATION"); d != "" {
		if p, err := time.ParseDuration(d); err == nil {
			dur = p
		}
	}
	const frameSize = 1200
	ks := parseIntListEnv("FANOUT_KS", []int{128, 256, 512})
	log.Printf("\n=== Pure-QUIC PPS (paced, target=%d fps/stream, size=%dB, dur=%s, K=%v) ===", targetFPS, frameSize, dur, ks)
	log.Printf("%-6s %-12s %-14s %-12s", "K", "target_agg", "delivered_agg", "ratio")
	for _, K := range ks {
		b.Run(fmt.Sprintf("K=%d", K), func(b *testing.B) {
			delivered, _ := pureQUICPPSRun(b, cert, pool, K, frameSize, targetFPS, dur)
			b.ReportMetric(delivered, "fps")
			target := float64(targetFPS) * float64(K)
			ratio := 0.0
			if target > 0 {
				ratio = delivered / target * 100
			}
			log.Printf("%-6d %-12.0f %-14.0f %-12.1f", K, target, delivered, ratio)
		})
	}
}

// pureQUICPPSRun runs one paced pure-QUIC fan-out measurement. Returns aggregate
// delivered frames/sec and frames "sent" (target aggregate).
func pureQUICPPSRun(tb testing.TB, cert tls.Certificate, pool *x509.CertPool, K, frameSize, targetFPS int, dur time.Duration) (float64, float64) {
	tb.Helper()
	addr := chainFreeAddr(tb)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{pureQUICALPN}, MinVersion: tls.VersionTLS13}
	clientTLS := &tls.Config{RootCAs: pool, NextProtos: []string{pureQUICALPN}, MinVersion: tls.VersionTLS13}
	quicCfg := &quic.Config{EnableDatagrams: true, MaxIdleTimeout: 30 * time.Second, KeepAlivePeriod: 5 * time.Second}

	ln, err := quic.ListenAddr(addr, serverTLS, quicCfg)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = ln.Close() })

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 15*time.Second)

	var totalBytes atomic.Uint64
	var dialed, opened, accSess, accStr atomic.Int32
	var clientWG sync.WaitGroup
	clientWG.Add(K)
	for range K {
		go func() {
			defer clientWG.Done()
			sess, err := quic.DialAddr(dialCtx, addr, clientTLS, quicCfg)
			if err != nil {
				return
			}
			dialed.Add(1)
			str, err := sess.AcceptUniStream(dialCtx)
			if err != nil {
				_ = sess.CloseWithError(0, "setup-failed")
				return
			}
			opened.Add(1)
			buf := make([]byte, 64*1024)
			for {
				n, err := str.Read(buf)
				if n > 0 {
					totalBytes.Add(uint64(n))
				}
				if err != nil {
					return
				}
			}
		}()
	}

	// Server: accept K sessions and their client-opened streams.
	var conns []*quic.Conn
	streams := make([]*quic.SendStream, 0, K)
	for len(streams) < K {
		sess, err := ln.Accept(dialCtx)
		if err != nil {
			break
		}
		conns = append(conns, sess)
		accSess.Add(1)
		// Server opens a uni-stream to this client (matches the relay's egress:
		// relay opens uni-streams to subscribers).
		str, err := sess.OpenUniStreamSync(dialCtx)
		if err != nil {
			continue
		}
		accStr.Add(1)
		streams = append(streams, str)
	}

	// Paced independent writers: one goroutine per stream, ticker-paced.
	runCtx, runCancel := context.WithTimeout(context.Background(), dur)
	payload := make([]byte, frameSize)
	period := time.Duration(float64(time.Second) / float64(targetFPS))
	if period <= 0 {
		period = time.Millisecond
	}
	var writerWG sync.WaitGroup
	writerWG.Add(len(streams))
	for _, s := range streams {
		s := s
		go func() {
			defer writerWG.Done()
			ticker := time.NewTicker(period)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
					if _, err := s.Write(payload); err != nil {
						return
					}
				}
			}
		}()
	}

	// Write phase bounded by runCtx (dur).
	<-runCtx.Done()
	runCancel()
	// Hang-proof teardown: close sessions (unblocks any Write/Read) + cancel dials.
	for _, c := range conns {
		_ = c.CloseWithError(0, "done")
	}
	dialCancel()
	writerWG.Wait()
	clientWG.Wait()

	delivered := float64(totalBytes.Load()) / float64(frameSize) / dur.Seconds()
	target := float64(targetFPS) * float64(K)
	log.Printf("diag K=%d dialed=%d opened=%d accSess=%d accStr=%d bytes=%d",
		K, dialed.Load(), opened.Load(), accSess.Load(), accStr.Load(), totalBytes.Load())
	return delivered, target
}
