---
title: doctor
description: Explain the relay's effective runtime configuration — read-only.
weight: 7
---

Explains the relay's *effective* runtime configuration and why — read-only,
it changes nothing. It doesn't run a server or connect to a relay over the
network; it only reads local environment variables.

## Usage

```
qumo doctor
```

Takes no flags or arguments.

## Example

```console
$ qumo doctor
qumo doctor — effective runtime configuration

GC target (garbage collector)
  Inputs:
    GOGC        = (unset)
    RELAY_GOGC  = (unset)
    GOMEMLIMIT  = (unset)
  Effective:    100%  (source: runtime default)
  Why:          neither GOGC nor RELAY_GOGC is set; the relay leaves the runtime default (100) in place. Set RELAY_GOGC on high-fan-out hosts to lift the session ceiling.
  Guidance:     A fan-out relay's goroutine stacks dominate RSS, so GC-scan CPU
                grows with session count and becomes the ceiling. On big-memory
                hosts pushing >15K sessions, set RELAY_GOGC (600–1600 reached
                ~18–20K on an 8-core host). GOGC always overrides. Do not set
                GOMEMLIMIT for this workload.
```

Because it only reads the environment, you can check what a setting would
resolve to before committing it anywhere:

```console
$ RELAY_GOGC=800 qumo doctor
...
  Effective:    800%  (source: RELAY_GOGC)
  Why:          RELAY_GOGC selected; the relay raises the GC target to cut GC-scan CPU for its large stable live set.
```

## Configuration

Takes no configuration of its own — it *inspects* the relay's: the effective GC
target (which of `GOGC` and `RELAY_GOGC` won, or the runtime default, and why),
plus guidance for high-fan-out deployments. `GOMEMLIMIT` is shown as an input
and warned about when set, but is deliberately not a candidate — capping memory
forces constant GC and collapses the relay's throughput.

## See also

- [Configuration → Capacity]({{< relref "../configuration" >}}#capacity) — the variables it reports on.
- [Observability]({{< relref "../observability" >}}) — full example output, plus `/health` and `/metrics`.
