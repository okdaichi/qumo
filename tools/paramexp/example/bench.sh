#!/usr/bin/env bash
# Example benchmark script for paramexp (portable: uses awk, not bc).
#
# Receives parameters as env vars (PARAM_WORKERS, PARAM_BUFFER, PARAM_BATCH,
# PARAM_JITTER). Outputs one JSON line of metrics on stdout.
#
# In a real setup this launches qumo (or any system) with the given parameters,
# runs a workload, and reports measured metrics. Here we synthesize a response
# surface for demonstration:
#   - throughput: concave in workers (knee ~4-8), rises with buffer (log) and
#     batch, falls with jitter.
#   - latency p99: rises with workers and jitter, falls with buffer.
#   - loss: ~0 below the workers knee, rises sharply above it.
#   - cpu: proportional to workers.

set -euo pipefail

workers="${PARAM_WORKERS:-1}"
buffer="${PARAM_BUFFER:-64KB}"
batch="${PARAM_BATCH:-1}"
jitter="${PARAM_JITTER:-0}"

buf_kb=$(printf '%s' "$buffer" | sed 's/KB//')

awk -v w="$workers" -v b="$buf_kb" -v ba="$batch" -v j="$jitter" 'BEGIN {
  e = 2.7182818284590452
  l2 = 0.6931471805599453
  # throughput: 100 * (1 - e^{-w/3}) * (1 + 0.3*log(b)/log(2)) * (1 + 0.1*(ba-1)) * (1 - 0.5*j)
  thr = 100 * (1 - e^(-w/3.0)) * (1 + 0.3 * log(b)/l2) * (1 + 0.1*(ba-1)) * (1 - 0.5*j)
  # latency p99 ms: 5 + 0.5*w + 30/b + 10*j
  lat = 5.0 + w*0.5 + 30.0/b + 10.0*j
  # loss pct: 0 below the knee (w<=4), rises above
  loss = (w <= 4) ? 0.0 : (w-4)*15.0
  cpu = w*12.5
  mem = b*0.1 + w*5
  printf "{\"throughput_fps\": %.1f, \"latency_p99_ms\": %.2f, \"loss_pct\": %.1f, \"cpu_pct\": %.1f, \"memory_mb\": %.1f}\n", thr, lat, loss, cpu, mem
}'
