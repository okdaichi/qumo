// Command latency-probe measures end-to-end frame delivery latency against a
// qumo relay. It subscribes to a trickle-published track (like the one produced
// by `qumo loadgen publish`), reads frames for a hold duration, extracts the
// publisher's embedded UnixNano timestamp from bytes 8-15 of each frame body,
// computes latency = arrival_time - publish_time, and reports percentiles.
//
// Usage:
//
//	go run ./bench-multiproc/tools/latency-probe/ \
//	    --relay 127.0.0.1:4434 \
//	    --ca bench-multiproc/cert.pem \
//	    --hold 10s
//
// --ca is required unless --insecure is set (for a self-signed dev relay).
//
// The tool exits after printing the latency summary to stdout. It also appends a
// JSONL record to the --results directory if provided.
//
// Frame format (compatible with loadgen publish and single_relay_bench_test.go):
//
//	[0:8]  = group sequence (uint64 BE)
//	[8:16] = publish UnixNano timestamp (uint64 BE)
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
)

const (
	defaultPath  = "/bench/carry"
	defaultTrack = "data"
	headerBytes  = 16 // [8 seq][8 publish_unix_nano]
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	relay    string
	caFile   string
	insecure bool
	path     string
	track    string
	hold     time.Duration
	settle   time.Duration // ramp-up exclusion window
	results  string        // optional JSONL output dir
}

type result struct {
	Samples  int           `json:"samples"`
	Min      time.Duration `json:"min"`
	P25      time.Duration `json:"p25"`
	P50      time.Duration `json:"p50"`
	P75      time.Duration `json:"p75"`
	P95      time.Duration `json:"p95"`
	P99      time.Duration `json:"p99"`
	Max      time.Duration `json:"max"`
	Mean     time.Duration `json:"mean"`
	JitterMs float64       `json:"jitter_ms"` // (p95-p50) in ms
}

func run(args []string) error {
	fs := flag.NewFlagSet("latency-probe", flag.ContinueOnError)
	relay := fs.String("relay", "127.0.0.1:4433", "relay moqt address (host:port)")
	caFile := fs.String("ca", "", "PEM file of the relay's TLS cert/CA to trust (required unless --insecure)")
	insecure := fs.Bool("insecure", false, "skip relay TLS verification (dev; self-signed relay)")
	path := fs.String("path", defaultPath, "broadcast path")
	track := fs.String("track", defaultTrack, "track name")
	hold := fs.Duration("hold", 10*time.Second, "total measurement window")
	settle := fs.Duration("settle", 3*time.Second, "discard samples during ramp-up")
	results := fs.String("results", "", "optional dir to append a JSONL latency record")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *caFile == "" && !*insecure {
		return errors.New("--ca is required (or --insecure for a self-signed dev relay)")
	}
	if *hold <= 0 {
		return errors.New("--hold must be positive")
	}

	cfg := config{
		relay:    *relay,
		caFile:   *caFile,
		insecure: *insecure,
		path:     *path,
		track:    *track,
		hold:     *hold,
		settle:   *settle,
		results:  *results,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	res, err := measure(ctx, cfg)
	if err != nil {
		return err
	}
	printResult(cfg, res)

	if cfg.results != "" {
		if err := emitJSONL(cfg.results, cfg, res); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write JSONL: %v\n", err)
		}
	}
	return nil
}

// measure subscribes, reads frames for hold duration, returns latency percentiles.
func measure(ctx context.Context, cfg config) (result, error) {
	tlsCfg, err := probeTLSConfig(cfg.caFile, cfg.insecure)
	if err != nil {
		return result{}, err
	}

	quicCfg := &quic.Config{EnableDatagrams: true, MaxIncomingUniStreams: 1 << 20, MaxIncomingStreams: 1 << 20}

	// Dial
	sess, err := (&moqt.Dialer{TLSConfig: tlsCfg, QUICConfig: quicCfg}).Dial(ctx, "moqt://"+cfg.relay, moqt.NewTrackMux(0))
	if err != nil {
		return result{}, fmt.Errorf("dial relay %s: %w", cfg.relay, err)
	}
	defer sess.CloseWithError(moqt.NoError, "done")

	// Subscribe
	tr, err := sess.Subscribe(ctx, moqt.BroadcastPath(cfg.path), moqt.TrackName(cfg.track), nil)
	if err != nil {
		return result{}, fmt.Errorf("subscribe to %s/%s: %w", cfg.path, cfg.track, err)
	}
	defer tr.Close()

	// Settle window: discard ramp-up frames before they inflate percentiles.
	settleUntil := time.Now().Add(cfg.settle)
	// Collect latencies for hold duration (after settle).
	collectUntil := time.Now().Add(cfg.settle + cfg.hold)
	buf := moqt.NewFrame(1500)
	var lats []float64 // milliseconds
	var totalFrames int

	for time.Now().Before(collectUntil) {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			break
		}
		for frame := range gr.Frames(buf) {
			totalFrames++
			body := frame.Body()
			if len(body) < headerBytes {
				continue
			}
			// Skip frames during settle window
			if time.Now().Before(settleUntil) {
				continue
			}
			pubNs := int64(binary.BigEndian.Uint64(body[8:16]))
			lat := time.Since(time.Unix(0, pubNs))
			lats = append(lats, lat.Seconds()*1000) // store as ms
		}
	}

	if len(lats) == 0 {
		return result{}, fmt.Errorf("no frames received within %s (settle=%s, total_frames=%d)",
			cfg.hold, cfg.settle, totalFrames)
	}

	return computeResult(lats), nil
}

// computeResult sorts latency samples and returns percentiles.
func computeResult(ms []float64) result {
	sort.Float64s(ms)
	n := len(ms)

	r := result{
		Samples: n,
		Min:     durationMs(ms[0]),
		Max:     durationMs(ms[n-1]),
	}

	// Compute mean
	var sum float64
	for _, v := range ms {
		sum += v
	}
	r.Mean = durationMs(sum / float64(n))

	// Percentiles using linear interpolation
	r.P25 = durationMs(percentile(ms, 25))
	r.P50 = durationMs(percentile(ms, 50))
	r.P75 = durationMs(percentile(ms, 75))
	r.P95 = durationMs(percentile(ms, 95))
	r.P99 = durationMs(percentile(ms, 99))
	r.JitterMs = math.Max(0, r.P95.Seconds()*1000-r.P50.Seconds()*1000)

	return r
}

// percentile returns the p-th percentile from a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func durationMs(ms float64) time.Duration {
	return time.Duration(ms * float64(time.Millisecond))
}

func printResult(cfg config, r result) {
	fmt.Printf("latency-probe: relay=%s path=%s hold=%s settle=%s\n",
		cfg.relay, cfg.path, cfg.hold, cfg.settle)
	fmt.Printf("  samples : %d\n", r.Samples)
	fps := float64(r.Samples) / cfg.hold.Seconds()
	fmt.Printf("  fps     : %.1f\n", fps)
	fmt.Printf("  min     : %s\n", r.Min.Round(time.Microsecond))
	fmt.Printf("  p25     : %s\n", r.P25.Round(time.Microsecond))
	fmt.Printf("  p50     : %s\n", r.P50.Round(time.Microsecond))
	fmt.Printf("  p75     : %s\n", r.P75.Round(time.Microsecond))
	fmt.Printf("  p95     : %s\n", r.P95.Round(time.Microsecond))
	fmt.Printf("  p99     : %s\n", r.P99.Round(time.Microsecond))
	fmt.Printf("  max     : %s\n", r.Max.Round(time.Microsecond))
	fmt.Printf("  mean    : %s\n", r.Mean.Round(time.Microsecond))
	fmt.Printf("  jitter  : %.2f ms\n", r.JitterMs)
}

// jsonlRecord is the latency record schema appended to results.jsonl.
type jsonlRecord struct {
	Bench    string  `json:"bench"`
	Group    string  `json:"group"`
	Relay    string  `json:"relay"`
	Path     string  `json:"path"`
	Hold     string  `json:"hold"`
	Samples  int     `json:"samples"`
	MinMs    float64 `json:"min_ms"`
	P25Ms    float64 `json:"p25_ms"`
	P50Ms    float64 `json:"p50_ms"`
	P75Ms    float64 `json:"p75_ms"`
	P95Ms    float64 `json:"p95_ms"`
	P99Ms    float64 `json:"p99_ms"`
	MaxMs    float64 `json:"max_ms"`
	MeanMs   float64 `json:"mean_ms"`
	JitterMs float64 `json:"jitter_ms"`
}

func emitJSONL(dir string, cfg config, r result) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	rec := jsonlRecord{
		Bench:    "latency-probe",
		Group:    "e2e",
		Relay:    cfg.relay,
		Path:     cfg.path,
		Hold:     cfg.hold.String(),
		Samples:  r.Samples,
		MinMs:    r.Min.Seconds() * 1000,
		P25Ms:    r.P25.Seconds() * 1000,
		P50Ms:    r.P50.Seconds() * 1000,
		P75Ms:    r.P75.Seconds() * 1000,
		P95Ms:    r.P95.Seconds() * 1000,
		P99Ms:    r.P99.Seconds() * 1000,
		MaxMs:    r.Max.Seconds() * 1000,
		MeanMs:   r.Mean.Seconds() * 1000,
		JitterMs: r.JitterMs,
	}
	return json.NewEncoder(f).Encode(rec)
}

// probeTLSConfig builds the relay TLS config: verify against caFile's trust
// anchor, or skip verification when insecure (a self-signed dev relay). Mirrors
// qumo's client TLS convention — verification is the default, --insecure is the
// explicit escape hatch.
func probeTLSConfig(caFile string, insecure bool) (*tls.Config, error) {
	tc := &tls.Config{NextProtos: []string{moqt.NextProtoMOQ}, MinVersion: tls.VersionTLS13}
	switch {
	case insecure:
		tc.InsecureSkipVerify = true
	case caFile != "":
		pemCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read --ca %q: %w", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemCert) {
			return nil, fmt.Errorf("no certificates found in --ca %q", caFile)
		}
		tc.RootCAs = pool
	}
	return tc, nil
}
