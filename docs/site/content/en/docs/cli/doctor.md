---
title: doctor
description: Explain the relay's effective runtime configuration — read-only.
weight: 6
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

```bash
qumo doctor                      # what the current environment resolves to

RELAY_GOGC=800 qumo doctor       # check a setting before applying it
```

See [Observability → qumo doctor]({{< relref "../observability" >}}#qumo-doctor)
for full example output.

## Configuration

Takes no configuration of its own — it *inspects* the relay's, currently the
effective GC target (which of `GOGC`, `RELAY_GOGC`, and `GOMEMLIMIT` won, and
why), with guidance for high-fan-out deployments.

## See also

- [Configuration → Capacity]({{< relref "../configuration" >}}#capacity) — the variables it reports on.
- [Observability]({{< relref "../observability" >}}) — full example output, plus `/health` and `/metrics`.
