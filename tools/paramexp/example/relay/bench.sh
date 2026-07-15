#!/usr/bin/env bash
# paramexp bench harness for the qumo relay fan-out benchmark.
#
# Receives the parameter vector as PARAM_* env vars (set by paramexp), maps them
# to the relay integration-test env knobs, runs BenchmarkRelayChain_FanoutSweep
# at the given fan-out K, and prints ONE JSON line of metrics on stdout:
#   {"loss_pct":..,"latency_p99_ms":..,"mbps":..,"fairness":..,"score":..}
# where score = mbps * fairness * (1 - loss/100) (higher is better; paramexp
# maximizes the objective).
#
# Must run from the qumo repo root (it cds there via git). Each vector is a full
# integration benchmark, so the full sweep belongs on the nightly Linux bench job.
#
# This script is deliberately tolerant: a failed/missing measurement degrades to
# a worst-case record rather than aborting, so one bad vector never corrupts the
# exploration. (No `set -e`.)

cd "$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0

export RELAY_RING="${PARAM_RING:-8}"
export RELAY_FRAME="${PARAM_FRAME:-1500}"
export RELAY_NOTIFY_TIMEOUT_MS="${PARAM_NOTIFY_MS:-1}"
K="${PARAM_FANOUT_K:-16}"

# BENCH_RESULTS_DIR must be absolute: `go test` runs the binary in the package dir.
RESULTS="$(mktemp -d 2>/dev/null)" || exit 0
trap 'rm -rf "$RESULTS"' EXIT
export BENCH_RESULTS_DIR="$RESULTS"
export FANOUT_KS="$K"

# Run the single-K fan-out sweep. Non-fatal: a failed run (OOM/timeout/flake)
# surfaces as a missing record below → worst-case score 0, not an abort.
go test -tags=integration -bench='RelayChain_FanoutSweep$' -benchtime=1x -cpu=1 \
  -timeout=600s ./internal/relay/ >/dev/null 2>&1

# Extract the record for this K (config:"K=$K") and emit one JSON line.
rec="$(grep -m1 "\"config\":\"K=$K\"" "$RESULTS/results.jsonl" 2>/dev/null)"
if [ -z "$rec" ]; then
  printf '{"loss_pct":100,"latency_p99_ms":0,"mbps":0,"fairness":0,"score":0}\n'
  exit 0
fi

fval() { echo "$rec" | grep -oE "\"$1\":[0-9.]+" | head -1 | cut -d: -f2 || true; }
loss="$(fval loss_pct)"; [ -n "$loss" ] || loss=100
p99="$(fval p99_ms)";    [ -n "$p99" ] || p99=0
mbps="$(fval mbps)";     [ -n "$mbps" ] || mbps=0
fair="$(fval fairness)"; [ -n "$fair" ] || fair=0
jit="$(fval jitter_ms)"; [ -n "$jit" ] || jit=0

score="$(awk "BEGIN{printf \"%.4f\", ($mbps)*($fair)*(1-($loss)/100)}" 2>/dev/null)"
[ -n "$score" ] || score=0
printf '{"loss_pct":%s,"latency_p99_ms":%s,"mbps":%s,"fairness":%s,"jitter_ms":%s,"score":%s}\n' \
  "$loss" "$p99" "$mbps" "$fair" "$jit" "$score"
exit 0
