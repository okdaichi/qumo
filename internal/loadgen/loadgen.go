// Package loadgen drives an out-of-process capacity load against a running qumo
// relay. It has two subcommands:
//
//	loadgen publish    — one trickle publisher (a small group every 1/GPS s)
//	loadgen subscribe  — ramp N subscriber sessions and measure the hold
//
// The point of running out-of-process is measurement fidelity: the earlier
// in-process capacity benchmark ran the relay and all N clients in one process
// on shared cores, so client-side QUIC-handshake CPU — not the relay — capped
// establishment (~6K on an 8-core VM). It was removed in favor of this tool.
// Here the relay is a separate process (ideally a separate host), and
// `subscribe` reports the
// RELAY's own per-session cost by scraping its /metrics endpoint
// (go_goroutines, process_resident_memory_bytes, qumo_relay_sessions_active)
// before and after the ramp — the number reflects the relay, not the load
// generator.
//
// `subscribe` emits a capacity-group JSONL record (when --results is set) in the
// same schema the relay-bench dashboard reads, so a sweep lands in the same
// consolidated index.html as the rest of the benchmarks.
package loadgen

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/relay"
)

// Run dispatches a loadgen subcommand. It is the package entrypoint wired into
// the top-level `qumo loadgen` command.
func Run(args []string) error {
	if len(args) < 1 {
		usage(os.Stderr)
		return errors.New("loadgen: a subcommand is required (subscribe|publish)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "subscribe":
		return runSubscribe(rest)
	case "publish":
		return runPublish(rest)
	case "sweep":
		return runSweep(rest)
	case "-h", "--help", "help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("loadgen: unknown subcommand %q", sub)
	}
}

func usage(w io.Writer) {
	_, _ = io.WriteString(w, `Usage: qumo loadgen <subcommand> [flags]

Out-of-process capacity load against a running qumo relay.

Subcommands:
  publish    Publish one trickle track to the relay (keep running during a sweep)
  subscribe  Ramp N subscriber sessions and measure the relay's hold + per-session cost
  sweep      Run a session-count sweep (publisher + subscribe per point), optionally
             starting a local relay subprocess (--start-relay); emits a dashboard-ready
             capacity JSONL. Point it at a remote relay for a true two-host run.

Run "qumo loadgen <subcommand> -h" for that subcommand's flags.
`)
}

// ---- shared dial config ----

// dialTarget holds the connection parameters shared by both subcommands.
type dialTarget struct {
	relay   string // moqt dial target, host:port
	metrics string // relay /metrics URL (subscribe only)
	path    string
	track   string
	tls     *tls.Config
	quic    *quic.Config
}

// newTarget builds a dialTarget that trusts pool. An empty metrics URL defaults
// to http://<relay>/metrics.
func newTarget(relay, metrics, path, track string, pool *x509.CertPool, idle, keepalive time.Duration) dialTarget {
	m := metrics
	if m == "" {
		m = "http://" + relay + "/metrics"
	}
	return dialTarget{
		relay:   relay,
		metrics: m,
		path:    path,
		track:   track,
		tls: &tls.Config{
			RootCAs:    pool,
			NextProtos: []string{moqt.NextProtoMOQ},
			MinVersion: tls.VersionTLS13,
		},
		quic: &quic.Config{
			EnableDatagrams: true,
			KeepAlivePeriod: keepalive,
			MaxIdleTimeout:  idle,
		},
	}
}

// bindCommon registers the flags common to publish/subscribe on fs and returns a
// finalizer that validates them and builds the TLS/QUIC config after Parse.
func bindCommon(fs *flag.FlagSet) func() (dialTarget, error) {
	relay := fs.String("relay", "127.0.0.1:4433", "relay moqt address (host:port)")
	metrics := fs.String("metrics", "", "relay /metrics URL (default http://<relay>/metrics)")
	caFile := fs.String("ca", "", "PEM file of the relay's TLS cert/CA to trust (required)")
	path := fs.String("path", "/bench/carry", "broadcast path")
	track := fs.String("track", "data", "track name")
	idle := fs.Duration("idle-timeout", 30*time.Second, "QUIC max idle timeout")
	keepalive := fs.Duration("keepalive", 5*time.Second, "QUIC keep-alive period")
	return func() (dialTarget, error) {
		if *caFile == "" {
			return dialTarget{}, errors.New("--ca is required (path to the relay's PEM cert to trust)")
		}
		pool, err := loadCAPool(*caFile)
		if err != nil {
			return dialTarget{}, err
		}
		return newTarget(*relay, *metrics, *path, *track, pool, *idle, *keepalive), nil
	}
}

// loadCAPool reads a PEM cert file into a pool. The relay's self-signed cert is
// its own issuer, so passing the cert itself is sufficient to trust it.
func loadCAPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read --ca %q: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in --ca %q", caFile)
	}
	return pool, nil
}

func dialer(t dialTarget) *moqt.Dialer {
	return &moqt.Dialer{TLSConfig: t.tls, QUICConfig: t.quic}
}

// ---- publish ----

func runPublish(args []string) error {
	fs := flag.NewFlagSet("loadgen publish", flag.ContinueOnError)
	finalize := bindCommon(fs)
	gps := fs.Float64("gps", 0.5, "groups per second (trickle rate)")
	size := fs.Int("size", 64, "frame size in bytes (min 16)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target, err := finalize()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	slog.Info("loadgen publishing", "relay", target.relay, "path", target.path, "gps", *gps, "size", *size)
	return publishTrickle(ctx, target, *gps, *size)
}

// publishTrickle publishes one track that emits a small group every 1/gps s,
// blocking until ctx is cancelled. Shared by `publish` and `sweep`.
func publishTrickle(ctx context.Context, target dialTarget, gps float64, size int) error {
	if size < 16 {
		size = 16
	}
	interval := time.Duration(float64(time.Second) / gps)
	mux := moqt.NewTrackMux(moqt.NewHopID())
	mux.PublishFunc(ctx, moqt.BroadcastPath(target.path), func(tw *moqt.TrackWriter) {
		defer tw.Close()
		payload := make([]byte, size)
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
				fr := moqt.NewFrame(size)
				_, _ = fr.Write(payload)
				_ = gw.WriteFrame(fr)
				_ = gw.Close()
			}
		}
	})
	sess, err := dialer(target).Dial(ctx, "moqt://"+target.relay, mux)
	if err != nil {
		return fmt.Errorf("dial relay %s: %w", target.relay, err)
	}
	defer sess.CloseWithError(moqt.NoError, "done")
	<-ctx.Done()
	return nil
}

// ---- subscribe ----

func runSubscribe(args []string) error {
	fs := flag.NewFlagSet("loadgen subscribe", flag.ContinueOnError)
	finalize := bindCommon(fs)
	sessions := fs.Int("sessions", 1000, "number of subscriber sessions")
	hold := fs.Duration("hold", 30*time.Second, "how long to hold sessions after establishment")
	resultsDir := fs.String("results", "", "dir to append a capacity JSONL record (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target, err := finalize()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	res, err := runCarry(ctx, target, *sessions, *hold)
	if err != nil {
		return err
	}
	report(target, *sessions, res)
	if *resultsDir != "" {
		if err := emitJSONL(*resultsDir, *sessions, res); err != nil {
			slog.Warn("loadgen results emission failed", "err", err)
		}
	}
	return nil
}

// carryResult is the measured outcome of a subscribe run. All resource deltas
// are the RELAY's (scraped from /metrics), not the load generator's.
type carryResult struct {
	connected       int
	receiving       int
	relayGoros      int     // go_goroutines delta on the relay
	relayRSSMB      float64 // process_resident_memory_bytes delta on the relay, MB
	relaySessions   float64 // qumo_relay_sessions_active at steady state
	metricsScraped  bool    // false if the relay /metrics could not be read
	perSessionKB    float64 // relayRSSMB*1024 / connected
	perSessionGoros float64 // relayGoros / connected
}

func runCarry(ctx context.Context, target dialTarget, sessions int, hold time.Duration) (carryResult, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	base, baseErr := fetchMetrics(ctx, client, target.metrics)
	if baseErr != nil {
		slog.Warn("relay /metrics unreadable; per-session cost will be unavailable", "url", target.metrics, "err", baseErr)
	}

	// Subscribers live under subCtx: they hold until we cancel it (right after
	// the steady-state scrape), then return and record their result. A generous
	// safety deadline guarantees they can never outlive the run.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	safety := hold + 60*time.Second

	// connCount is both the connected tally and the drain signal (subscribeOne
	// increments it on a successful subscribe); receivingCount tallies sessions
	// that got >=1 frame. Both are atomics rather than shared slices read after
	// a timed drain, so the post-drain read is race-free even if a straggler
	// subscriber is still exiting past the drain deadline.
	var connCount atomic.Int64
	var receivingCount atomic.Int64
	var wg sync.WaitGroup

	// Launch all sessions at once (burst). The exponential backoff in
	// dialWithRetry spreads out the QUIC handshake load so the relay is not
	// overwhelmed by synchronized re-dials.
	for range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if subscribeOne(subCtx, target, safety, &connCount) > 0 {
				receivingCount.Add(1)
			}
		}()
	}

	// Hold: wait for sessions to connect (backoff handles retries), then hold
	// at steady state before the second scrape.
	settleFor(ctx, &connCount, sessions, 30*time.Second)
	select {
	case <-ctx.Done():
	case <-time.After(hold):
	}

	// Scrape while the sessions are still established, THEN release the
	// subscribers so their per-session results (connected/frames) are recorded.
	steady, steadyErr := fetchMetrics(ctx, client, target.metrics)
	subCancel()
	drain(&wg, 20*time.Second)

	res := carryResult{
		connected: int(connCount.Load()),
		receiving: int(receivingCount.Load()),
	}
	if baseErr == nil && steadyErr == nil {
		res.metricsScraped = true
		res.relayGoros = int(steady.goroutines - base.goroutines)
		res.relayRSSMB = (steady.rssBytes - base.rssBytes) / (1024 * 1024)
		res.relaySessions = steady.sessionsActive
		if res.connected > 0 {
			res.perSessionKB = res.relayRSSMB * 1024 / float64(res.connected)
			res.perSessionGoros = float64(res.relayGoros) / float64(res.connected)
		}
	}

	return res, nil
}

// subscribeOne dials one session, subscribes, and counts received frames until
// the deadline or cancellation. It increments connCount once on a successful
// subscribe (so connCount is the "connected" tally) and returns the frame count
// (0 if it never subscribed).
//
// Dial failures are retried with exponential backoff + jitter so that a burst
// of simultaneous subscriber connections (common in fan-out benchmarks) does
// not trigger a thundering-herd of synchronized re-dials that overwhelms the
// relay's handshake capacity.
func subscribeOne(ctx context.Context, target dialTarget, dur time.Duration, connCount *atomic.Int64) int {
	dctx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()
	sess, err := dialWithRetry(dctx, target)
	if err != nil {
		return 0
	}
	defer sess.CloseWithError(moqt.NoError, "done")
	tr, err := sess.Subscribe(dctx, moqt.BroadcastPath(target.path), moqt.TrackName(target.track), nil)
	if err != nil {
		return 0
	}
	connCount.Add(1)
	defer tr.Close()
	buf := moqt.NewFrame(1500)
	n := 0
	for {
		gr, err := tr.AcceptGroup(dctx)
		if err != nil {
			break
		}
		for range gr.Frames(buf) {
			n++
		}
	}
	return n
}

// dialWithRetry dials the relay with exponential backoff + jitter on failure.
// It retries until the dial succeeds or ctx is cancelled, using the relay
// package's DialBackoff to spread out synchronized re-dials across subscriber
// goroutines with the same algorithm the server-side maintainPeer uses.
func dialWithRetry(ctx context.Context, target dialTarget) (*moqt.Session, error) {
	var backoff = relay.DialBackoff{Base: 1 * time.Second, Max: 30 * time.Second}
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sess, err := dialer(target).Dial(ctx, "moqt://"+target.relay, moqt.NewTrackMux(0))
		if err == nil {
			return sess, nil
		}
		slog.Debug("loadgen dial failed, retrying", "relay", target.relay, "retry_attempt", backoff.Attempts()+1, "err", err)
		if !backoff.Wait(ctx) {
			return nil, ctx.Err()
		}
	}
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

// drain waits for all subscriber goroutines to finish, bounded by timeout so a
// stuck subscriber can't hang teardown (each also has its own safety deadline).
func drain(wg *sync.WaitGroup, timeout time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// ---- relay /metrics scraping ----

// relaySnapshot is the subset of the relay's /metrics we track.
type relaySnapshot struct {
	goroutines     float64
	rssBytes       float64
	sessionsActive float64
}

func fetchMetrics(ctx context.Context, client *http.Client, url string) (relaySnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return relaySnapshot{}, fmt.Errorf("build metrics request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return relaySnapshot{}, fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return relaySnapshot{}, fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return relaySnapshot{}, fmt.Errorf("read %s: %w", url, err)
	}
	return parseMetrics(string(body))
}

// parseMetrics extracts the three metrics of interest from Prometheus text
// exposition. Each is an unlabelled scalar, so the line is "name value".
func parseMetrics(text string) (relaySnapshot, error) {
	want := map[string]*float64{}
	var snap relaySnapshot
	want["go_goroutines"] = &snap.goroutines
	want["process_resident_memory_bytes"] = &snap.rssBytes
	want["qumo_relay_sessions_active"] = &snap.sessionsActive
	found := 0
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		name, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		dst, tracked := want[name]
		if !tracked {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			return relaySnapshot{}, fmt.Errorf("parse metric %q value %q: %w", name, val, err)
		}
		*dst = f
		found++
	}
	if found == 0 {
		return relaySnapshot{}, errors.New("no expected metrics found in exposition")
	}
	return snap, nil
}

// ---- reporting ----

// verdictFor is the hold criterion: at least 99% of offered sessions must be
// receiving frames. Shared by the console report and the JSONL record so they
// never disagree.
func verdictFor(receiving, sessions int) string {
	if receiving < int(float64(sessions)*0.99) {
		return "CANNOT-HOLD"
	}
	return "HOLDS"
}

func report(target dialTarget, sessions int, r carryResult) {
	verdict := verdictFor(r.receiving, sessions)
	fmt.Printf("loadgen subscribe → relay %s (path %s)\n", target.relay, target.path)
	fmt.Printf("  offered sessions : %d\n", sessions)
	fmt.Printf("  connected        : %d\n", r.connected)
	fmt.Printf("  receiving        : %d\n", r.receiving)
	if r.metricsScraped {
		fmt.Printf("  relay Δgoroutines: %d (%.1f/session)\n", r.relayGoros, r.perSessionGoros)
		fmt.Printf("  relay ΔRSS       : %.1f MB (%.1f KB/session)\n", r.relayRSSMB, r.perSessionKB)
		fmt.Printf("  relay sessions   : %.0f active (relay-reported)\n", r.relaySessions)
	} else {
		fmt.Printf("  relay cost       : unavailable (/metrics unreadable — see warning)\n")
	}
	fmt.Printf("  verdict          : %s\n", verdict)
}

// jsonlRecord is the capacity-group record the relay-bench dashboard reads, so a
// sweep lands in the same index.html. Resource fields are the RELAY's, not the
// load generator's.
type jsonlRecord struct {
	Bench        string  `json:"bench"`
	Group        string  `json:"group"`
	Config       string  `json:"config"`
	Sessions     int     `json:"sessions"`
	Connected    int     `json:"connected"`
	Receiving    int     `json:"receiving"`
	HeapMB       float64 `json:"heap_mb,omitempty"`
	Goros        int     `json:"goros,omitempty"`
	PerSessionKB float64 `json:"per_session_kb,omitempty"`
	Verdict      string  `json:"verdict"`
}

func emitJSONL(dir string, sessions int, r carryResult) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	verdict := verdictFor(r.receiving, sessions)
	rec := jsonlRecord{
		Bench: "loadgen", Group: "capacity",
		Config:    "out-of-process/S=" + strconv.Itoa(sessions),
		Sessions:  sessions,
		Connected: r.connected, Receiving: r.receiving,
		HeapMB: r.relayRSSMB, Goros: r.relayGoros,
		PerSessionKB: r.perSessionKB, Verdict: verdict,
	}
	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open results.jsonl: %w", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(rec); err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	return nil
}
