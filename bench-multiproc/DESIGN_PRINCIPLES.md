# Benchmark Design Principles

## 1. Client–Server Process Isolation

The system under test (relay/server) MUST always run in a separate OS process
from the load generator (client). This is the single most important design rule
for the qumo benchmark harness.

### Rationale

Sharing a Go runtime between client and server means the benchmark measures the
combined behaviour of both, not the server alone. A shared process also means
sharing:

- **Go scheduler** — client goroutines compete with server goroutines for P
  slots, inflating goroutine scheduling latency on the server side.
- **Garbage collector** — client allocations trigger GC cycles that pause the
  server, distorting tail-latency measurements and adding GC CPU overhead that
  does not exist in production.
- **Heap** — client memory pressure drives GC frequency higher than the server
  would experience alone.
- **Timers** — timer allocation and cancellation from client connections compete
  with the server's own timer heap.
- **Netpoller** — all network I/O (both client and server) goes through the
  same epoll/kqueue loop, coupling the two workloads.

Any contention in the client directly affects the server, making it impossible
to determine whether we are measuring the relay or the benchmark harness.

### The Evidence

In this project, the principle was confirmed experimentally:

| Subscriber model | P=2 X=1000 connected | Reliability |
|-----------------|---------------------|-------------|
| In-process goroutines | 1,218 / 2,000 (61%) | ❌ |
| Out-of-process subprocesses | 1,997 / 2,000 (99.85%) | ✅ |

The only variable was whether subscribers ran as goroutines inside the
controller (sharing the Go runtime with the relay) or as separate OS processes.
The goroutine mode introduced enough scheduler and GC noise to lose 39% of
connections. The subprocess mode, which faithfully isolates client from server,
achieved near-perfect connectivity.

### What Must Be Isolated

- **Relays** — every relay process (hub and each edge) runs in its own OS
  process. No relay may share a Go runtime with another relay.
- **Publisher** — runs in its own OS process via `qumo loadgen publish`.
- **Subscribers** — run in their own OS processes via `qumo loadgen subscribe`.
  Different subscriber batches may share a subprocess, but no subscriber
  may share a process with any relay.
- **Benchmark harness** — coordinates start/stop/scrape but does not generate
  or terminate significant load within its own process. It is a thin
  orchestrator, not a load generator.

### Enforcement

The `subscriber.go` package exports only `SubscribeGroupSubprocess` and
`PublishSubprocess` — there are no in-process client functions. All client
connections to relays go through separate OS processes. There is no flag to
toggle this behaviour.

## 2. Two-Step Benchmark Procedure

The benchmark follows a two-step procedure:

### Step 1: Establish the Single-Edge Baseline

With P=1, measure the maximum sustainable subscriber count. This is `Max(P=1)`
— the per-edge capacity baseline.

### Step 2: Measure Aggregate Scaling

Repeat for increasing P values (2, 3, 4, …). Each edge attempts approximately
`Max(P=1)` subscribers. The theoretical aggregate capacity is `P × Max(P=1)`.

The purpose of the benchmark is to determine how closely the implementation
approaches this theoretical scaling limit.

### Primary Metric: Scaling Efficiency

```
ScalingEfficiency = Connected / (P × Max(P=1))
```

The ideal result is ~100% for all P, indicating linear scaling.

## 3. No Shell Scripts for Benchmark Control

Shell scripts introduce a compatibility layer (MSYS2, Git Bash, WSL) that does
not exist in the production deployment environment. All benchmark orchestration
is implemented in Go and compiled to a native binary.

## 4. Collect Per-Edge Evidence

In a multi-process experiment, it is not enough to report aggregate metrics.
Every cell must verify that all edge processes actually participate in
forwarding. The `PrintEdgeDistribution` function reports per-edge subscriber
counts, egress bytes, CPU, and RSS, and flags edges that are idle.

## 5. Use the Same Workload Across Configurations

To compare P=1, P=2, P=4 meaningfully, keep subscribers-per-edge (X) constant
and let aggregate capacity be P × X. This isolates the effect of adding relay
processes from the effect of changing per-process load.
