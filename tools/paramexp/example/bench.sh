#!/usr/bin/env bash
# Example benchmark script for paramexp.
#
# Receives parameters as env vars (PARAM_WORKERS, PARAM_BUFFER, PARAM_BATCH).
# Outputs one JSON line of metrics on stdout.
#
# In a real setup, this would launch qumo (or any system) with the given
# parameters, run a workload, and report measured metrics. Here we simulate
# a synthetic response surface for demonstration.

set -euo pipefail

workers="${PARAM_WORKERS:-1}"
buffer="${PARAM_BUFFER:-64KB}"
batch="${PARAM_BATCH:-1}"

# Convert buffer string to KB
buf_kb=$(echo "$buffer" | sed 's/KB//')

# Synthetic model:
#   throughput increases with workers (diminishing returns, knee at 4-8)
#   throughput increases with buffer (logarithmic)
#   throughput increases with batch (linear, capped)
#   latency decreases with buffer, increases with workers
#   loss is 0 below the knee, rises sharply above

w="$workers"
b="$buf_kb"
ba="$batch"

# Throughput: concave in workers (knee at ~4-8), logarithmic in buffer
throughput=$(echo "scale=1; 100 * (1 - e(-$w/3.0)) * (1 + 0.3 * l($b)/l(2)) * (1 + 0.1*($ba - 1))" | bc -l 2>/dev/null || echo "100")
throughput=$(printf "%.0f" "$throughput")

# Latency p99: increases with workers, decreases with buffer
latency=$(echo "scale=2; 5.0 + $w * 0.5 + 30.0 / $b" | bc -l 2>/dev/null || echo "10")
latency=$(printf "%.2f" "$latency")

# Loss: 0 below knee, rises above
if [ "$w" -le 4 ]; then
  loss="0.0"
else
  loss=$(echo "scale=1; ($w - 4) * 15" | bc -l 2>/dev/null || echo "0")
  loss=$(printf "%.1f" "$loss")
fi

# CPU: proportional to workers
cpu=$(echo "scale=1; $w * 12.5" | bc -l 2>/dev/null || echo "50")

# Memory: proportional to buffer
mem=$(echo "scale=1; $b * 0.1 + $w * 5" | bc -l 2>/dev/null || echo "100")

# Output: one JSON line of metrics
cat <<EOF
{"throughput_fps": $throughput, "latency_p99_ms": $latency, "loss_pct": $loss, "cpu_pct": $cpu, "memory_mb": $mem}
EOF
