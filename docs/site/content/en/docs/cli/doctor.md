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

Takes no configuration of its own — it *inspects* the relay's, currently the
effective GC target (which of `GOGC`, `RELAY_GOGC`, and `GOMEMLIMIT` won, and
why), with guidance for high-fan-out deployments.

## See also

- [Configuration → Capacity]({{< relref "../configuration" >}}#capacity) — the variables it reports on.
- [Observability]({{< relref "../observability" >}}) — full example output, plus `/health` and `/metrics`.
