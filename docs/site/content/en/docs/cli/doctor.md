---
title: doctor
description: Explain the relay's effective runtime configuration — read-only.
weight: 6
---

Explains the relay's *effective* runtime configuration and why — read-only,
it changes nothing. It doesn't run a server or connect to a relay over the
network; it only reads local environment variables.

```bash
qumo doctor
```

## Configuration

Takes no configuration of its own — it *inspects* the relay's, currently the
effective GC target (which of `GOGC`, `RELAY_GOGC`, and `GOMEMLIMIT` won, and
why), with guidance for high-fan-out deployments. See
[Configuration → Capacity]({{< relref "../configuration" >}}#capacity) for
those variables.

See [Observability → qumo doctor]({{< relref "../observability" >}}#qumo-doctor)
for full example output.
