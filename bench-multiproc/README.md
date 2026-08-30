# Multi-Process Qumo Relay Benchmark

This directory contains the **authoritative benchmark harness** for measuring
qumo's multi-relay fan-out scaling. It answers the question:

> *Does running multiple qumo relay processes on a single VM improve aggregate
> fan-out capacity compared with a single qumo relay process?*

## Important: Native Linux Required

The benchmark controller and relay processes **must run on native Linux** for
authoritative results. WSL (Windows Subsystem for Linux) introduces syscall
translation overhead, UDP socket degradation, and process-count limits that
distort measurements — especially at P ≥ 6 edge processes.

| Environment | P=1..4 | P≥6 | Use case |
|-------------|--------|-----|----------|
| **Native Linux** | ✅ Authoritative | ✅ Authoritative | Final results |
| **WSL / MSYS2** | ✅ Functional | ❌ Unreliable | Development, iteration |

The pre-built binaries in `bin/` were built for Linux (WSL). For native Linux
you **must rebuild from source** (see [Build](#build) below).

## Overview

### Topology

The benchmark deploys a two-tier hub-and-edge topology:

```
Publisher
    |
   Hub                        (1 process, port 4433)
  / | \
 E0 E1 ... EP-1               (P edge processes, ports 4434+)
 |  |      |
Subscribers                   (per-edge, separate subprocesses)
```

- **Hub**: receives published data, forwards to all edge peers
- **Edges**: receive data from hub, fan out to subscribers
- **Subscribers**: out-of-process `qumo loadgen subscribe` instances
- **Publisher**: out-of-process `qumo loadgen publish` instance

### Process Isolation

This is the **most important design principle**. Every relay (hub + each edge)
runs in its own OS process. Subscribers run in separate OS processes — never
as goroutines inside the controller. This ensures:

- No shared Go runtime (scheduler, GC, heap, netpoller, timers)
- The benchmark measures **relay performance**, not benchmark-harness interference
- See [`DESIGN_PRINCIPLES.md`](DESIGN_PRINCIPLES.md) for the full rationale

### Benchmark Procedure

1. **Calibrate**: Run P=1 with increasing X to find `Max(P=1)` — the per-edge
   capacity baseline (Step 1 / `calibrate` command)
2. **Scale**: Run P=2, 3, 4, … with each edge attempting `Max(P=1)` subscribers
   (Step 2 / `sweep` command)

The primary metric is **scaling efficiency**:

```
ScalingEfficiency = Connected / (P × Max(P=1))
```

---

## Prerequisites

- **Native Linux** (Ubuntu 22.04+, Debian 12+, or equivalent; x86_64)
- **Go 1.27+** (the project Go version; check `go.mod`)
- **~/go2/go** installed (see [Install Go](#install-go) if needed)
- Enough **process slots** for: 1 controller + 1 hub + P edges + 1 publisher
  + (P × subscriber-subprocess‑per‑edge) = 2P + ~3 concurrent OS processes
- Enough **ephemeral ports** (each subscriber uses an ephemeral port per QUIC
  connection; at 12K subscribers this is ~12K ports — ensure `net.ipv4.ip_local_port_range` is sufficiently wide)

### Install Go

If Go 1.27+ is not installed at `~/go2/go`:

```bash
# Download Go 1.27+ for linux/amd64
wget https://go.dev/dl/go1.27.0.linux-amd64.tar.gz
rm -rf ~/go2 && mkdir -p ~/go2
tar -C ~/go2 -xzf go1.27.0.linux-amd64.tar.gz
# The binary is now at ~/go2/go/bin/go
export PATH="$HOME/go2/go/bin:$PATH"
```

Verify:

```bash
~/go2/go/bin/go version
# go version go1.27.0 linux/amd64
```

---

## Build

Build the qumo relay binary and the benchmark controller:

```bash
cd /path/to/qumo

# Build qumo relay
~/go2/go/bin/go build -o bench-multiproc/bin/qumo-linux .

# Build benchmark controller
cd bench-multiproc
~/go2/go/bin/go build -o bin/benchctl-linux ./cmd/benchctl/
```

---

## Quick Start

Run the full benchmark on a fresh native Linux VM (8+ cores recommended):

```bash
# 1. Calibrate — find per-edge capacity
./bin/benchctl-linux calibrate \
  --xlist "500 750 1000 1500 2000" \
  --hold 30s --gps 30 \
  --qumo ./bin/qumo-linux

# 2. Sweep — measure aggregate scaling
./bin/benchctl-linux sweep \
  --plist "1 2 4 6 8" \
  --xlist "1000" \
  --hold 30s --gps 30 \
  --qumo ./bin/qumo-linux \
  --ref-max-p1 <value_from_calibrate>
```

> **Note**: The `calibrate` command prints `Max(P=1)` at the end — pass that
> value to `--ref-max-p1` so the sweep correctly computes scaling efficiency.

---

## Commands

### `run` — Single Cell

Run one (P, X) configuration:

```bash
./bin/benchctl-linux run <P> <X> [flags]
```

Examples:

```bash
# 1 edge, 1000 subscribers, 30s hold
./bin/benchctl-linux run 1 1000 --hold 30s --gps 30 --qumo ./bin/qumo-linux

# 4 edges, 3000 subscribers each, with e2e latency probe
./bin/benchctl-linux run 4 3000 \
  --hold 30s --gps 30 \
  --latency-probe \
  --qumo ./bin/qumo-linux
```

### `sweep` — Multi-Cell Sweep

Run a matrix of P × X configurations:

```bash
./bin/benchctl-linux sweep [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--plist` | `"1 2 4"` | Space-separated edge counts to test |
| `--xlist` | `"1000"` | Space-separated subscribers-per-edge to test |
| `--ref-max-p1` | `0` | `Max(P=1)` from calibration; 0 = auto-detect |

The sweep runs every (P, X) combination, randomizes ports between cells to
avoid TIME_WAIT conflicts, and writes per-cell results to `results/results.jsonl`.

Example — sweep P=1..8 with X=1000:

```bash
./bin/benchctl-linux sweep \
  --plist "1 2 4 6 8" \
  --xlist "1000" \
  --hold 30s --gps 30 \
  --pin=false \
  --qumo ./bin/qumo-linux
```

### `calibrate` — Find Max(P=1)

Establish the per-edge capacity baseline:

```bash
./bin/benchctl-linux calibrate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--xlist` | `"500 750 1000 1500"` | Subscriber counts to test at P=1 |

Prints the best sustainable connected subscriber count as the per-edge ceiling.

Example:

```bash
./bin/benchctl-linux calibrate \
  --xlist "500 750 1000 1500 2000 3000" \
  --hold 30s --gps 30 \
  --qumo ./bin/qumo-linux
```

---

## Flags Reference

All flags apply to `run`, `sweep`, and `calibrate` (unless noted):

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--gps` | `float64` | `30` | Groups per second (publishing rate) |
| `--size` | `int` | `1200` | Frame payload bytes |
| `--hold` | `duration` | `30s` | How long subscribers stay connected |
| `--qumo` | `string` | auto-detect | Path to qumo binary |
| `--cert-dir` | `string` | auto-detect | Directory for TLS certs (auto-generated if missing) |
| `--results` | `string` | `../results/` | Output directory for JSONL + logs |
| `--hub-port` | `int` | `4433` | Hub listen port |
| `--edge-base` | `int` | `4434` | First edge listen port (edges use base, base+1, …) |
| `--pin` | `bool` | `true` | Pin relays to dedicated cores via `taskset` |
| `--latency-probe` | `bool` | `false` | Collect e2e latency (requires one extra subscriber) |

Sweep-only flags:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--plist` | `string` | `"1 2 4"` | Edge counts to sweep |
| `--xlist` | `string` | `"1000"` | Subscribers per edge to sweep |
| `--ref-max-p1` | `int` | `0` | Calibrated Max(P=1) for efficiency calculation |

Environment variables:

| Variable | Overridden by |
|----------|--------------|
| `BENCH_QUMO_BIN` | `--qumo` flag |
| `BENCH_CERT_DIR` | `--cert-dir` flag |

### Core Pinning

When `--pin=true` (default), the controller pins each relay to dedicated CPU
cores using `taskset -c`. The allocation reserves ~60% of available cores for
relays and the remainder for load generators. Disable with `--pin=false` on
systems where taskset is unavailable (containers, restricted environments).

---

## Output

### Per-Cell Output (stdout)

Each cell prints a summary line:

```
P=4 X=1000 total=4000 conn=3992 recv=99% cpu=1.97s rss=48MB sustained=true
```

| Field | Meaning |
|-------|---------|
| `P` | Edge count |
| `X` | Subscribers per edge |
| `total` | Total subscriber count = P × X |
| `conn` | Count of connected subscribers |
| `recv` | % of connected that are receiving frames |
| `cpu` | Aggregate edge CPU time (delta over measurement period) |
| `rss` | Peak edge RSS memory |
| `sustained` | Pass/fail (meets 95%+ connected, 95%+ receiving, all edges active) |

### JSONL Results (`results/results.jsonl`)

Structured results for analysis. Each line is a JSON object with all cell
metrics, per-edge breakdown, and optional e2e latency percentiles.

```jsonl
{"P":4,"X":1000,"total_subs":4000,"connected":3992,"receiving":3988,...}
```

Also included in each result: per-edge metrics (CPU, RSS, egress bytes,
subscriber skips, active subscribers), hub metrics, and a distribution
balance analysis.

### Latency Probe (`--latency-probe`)

When enabled, a dedicated pre-warmed subscriber connects to edge 0 **before**
the main subscriber batch and collects e2e latency samples. It reads the
publisher's embedded UnixNano timestamp from frame bytes [8:16] and reports:

| Metric | Description |
|--------|-------------|
| `latency_samples` | Number of frames with valid timestamps |
| `latency_p50_ms` | Median e2e latency (ms) |
| `latency_p95_ms` | 95th percentile |
| `latency_p99_ms` | 99th percentile |
| `latency_min_ms` | Minimum |
| `latency_max_ms` | Maximum |
| `latency_mean_ms` | Arithmetic mean |

> **Pre-warming**: The latency probe connects before the main subscriber
> thundering herd, ensuring it receives fresh frames with valid timestamps
> rather than stale buffered groups from the ring cache.

---

## Interpreting Results

### Scaling Efficiency

The sweep's final output includes a scaling efficiency table:

```
Scaling Efficiency = Connected / (P × Max(P=1))

P    X/edge     total_subs    connected    efficiency
1    1000       1000          1000         100.0%
2    1000       2000          1997         99.9%
4    1000       4000          3992         99.8%
6    1000       6000          5450         90.8%
8    1000       8000          5750         71.9%
```

| Efficiency | Interpretation |
|------------|---------------|
| ~100% | Linear scaling — multi-process isolation is working |
| 80–99% | Good scaling — some shared resource overhead |
| 50–79% | Moderate — bottleneck in shared infrastructure |
| <50% | Poor — fundamental bottleneck (process count too high for cores) |

### Per-Edge Distribution

The sweep also prints a per-edge breakdown to verify traffic is evenly
distributed:

```
  P=4 X=1000 total=4000 conn=3992 — Edge Breakdown:
    edge   subs-act conn    recv     egrMB    cpu_s   rssMB    skips
    0      1000     1000   1000     312.00   0.48    48       0
    1      1000     1000   1000     310.00   0.49    47       0
    2      1000     998    996      309.00   0.47    48       0
    3      1000     994    992      308.00   0.50    47       0
    → mean-edge: 998 subs, range: 994–1000, imbalance: 0.6%
    ✅ Distribution is balanced (imbalance <20%).
```

If any edge shows zero subscribers, receiving, or egress bytes, the cell is
flagged `inactive_edges` — the measurement is **invalid** because not all
relay processes are participating.

---

## Benchmark Methodology

### Controlled Variables

Keep these identical across all comparisons:

- Same VM hardware
- Same CPU allocation
- Same memory
- Same network interface
- Same workload (GPS, frame size, hold duration)
- Same qumo binary
- Same relay configuration (except process count)

### What We Measure

| Category | Metrics |
|----------|---------|
| **Capacity** | Total connected subscribers, total receiving subscribers |
| **Throughput** | Total delivered frames, aggregate egress bytes |
| **Latency** | e2e p50/p95/p99 (with `--latency-probe`) |
| **Resource** | Per-process CPU, RSS, heap, goroutines, GC stats |
| **Network** | Hub CPU (indicator of peer-forwarding bottleneck), per-edge bandwidth |

### Pass Criteria (Tier 1 — automated)

- Connected ≥ 95% of offered subscribers
- Receiving ≥ 95% of connected subscribers
- All edge processes have non-zero traffic (verified per-edge)

### Diagnostic Signals (Tier 2 — reviewed)

- Hub CPU usage (spike at high P → hub is the bottleneck)
- Per-edge imbalance >20% → uneven distribution
- GC CPU >5% → GC pressure affecting throughput
- UDP drops → kernel buffer exhaustion

---

## Previous Results Summary

On an 8-core WSL VM, the benchmark demonstrated:

| P | Subs/edge | Connected | Scaling efficiency | Note |
|:-:|:---------:|:---------:|:-----------------:|------|
| 1 | 1,000 | 1,000 | 100.0% | Baseline |
| 2 | 1,000 | 2,000 | 100.0% | Perfect linear |
| 4 | 1,000 | 3,992 | 99.8% | Near-perfect |
| 6+ | 1,000 | various | unreliable | WSL-limited; native Linux needed |

**Key findings:**

1. The relay code scales **near-perfectly linearly** up to at least P=4 on 8
   cores — aggregate capacity = P × per-edge ceiling
2. Each edge process should stay **≤2K subscribers** for low per-subscriber
   latency (beyond that, quic-go's per-stream creation cost creates a latency
   cliff)
3. The hub is **not a bottleneck** — at P=16 it used only ~17% of one core
4. **Native Linux** is required for P ≥ 6 measurements

---

## File Layout

```
bench-multiproc/
├── README.md                         ← this file
├── DESIGN_PRINCIPLES.md              ← benchmark design rationale
├── bin/
│   ├── benchctl-linux                ← pre-built controller binary (Linux/WSL)
│   └── qumo-linux                    ← pre-built qumo relay binary (Linux/WSL)
├── cmd/benchctl/main.go              ← controller entry point
├── controller/
│   ├── config.go                     ← Config, flag parsing, validation
│   ├── orchestrate.go                ← RunCell — one (P, X) experiment
│   ├── sweep.go                      ← RunSweep — multi-cell sweep
│   ├── subprocess.go                 ← relay lifecycle, readiness checks, port cleanup
│   ├── subscriber.go                 ← subprocess-based subscriber + publisher launcher
│   ├── metrics.go                    ← Prometheus /metrics scraping + parsing
│   ├── report.go                     ← result types, JSONL, table printer, efficiency analysis
│   ├── topology.go                   ← hub+edge topology builder, core allocation
│   └── cert.go                       ← self-signed TLS cert generation
├── tools/
│   └── latency-probe/main.go         ← standalone e2e latency measurement tool
├── results/                          ← output directory (JSONL + logs)
├── cert.pem / key.pem                ← auto-generated TLS certificates
├── gen-cert.sh                       ← (legacy) openssl-based cert generation
├── run-level.sh                      ← (legacy) bash orchestration script
├── run-sweep.sh                      ← (legacy) bash sweep script
└── analyze.sh                        ← (legacy) bash result parser
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `inactive_edges` at P≥6 | WSL process/socket limit | Run on native Linux |
| `connected < 50%` at P=4 X=3000 | Per-edge subs exceed ~2K ceiling; also may be memory-limited on WSL | Reduce X or increase P |
| Port in use errors | Stale relay processes | The controller's `killPortProcesses` handles this; if it fails, manually run `fuser -k <port>/tcp` |
| `taskset: failed to set affinity` | `--pin=true` on container without CAP_SYS_NICE | Use `--pin=false` |
| Subscriber subprocess crashes | Out of ephemeral ports | Check `sysctl net.ipv4.ip_local_port_range`; widen range with `sysctl -w net.ipv4.ip_local_port_range="15000 65000"` |

---

## References

- [`DESIGN_PRINCIPLES.md`](DESIGN_PRINCIPLES.md) — complete design rationale and
  experimental evidence for the process-isolation requirement
- [docs/README.md](../docs/README.md) — qumo project documentation
- [internal/relay/](../internal/relay/) — qumo relay implementation
