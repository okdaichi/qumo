---
title: relay
description: Start the MoQT relay server.
weight: 1
---

```
Usage: qumo relay [flags]

Start the MoQT relay server.

Flags:
  --role <hub|edge>  node topology role (default: flat / single-node)

All other configuration is via environment variables;
see relay-config.example.env for the full list.
```

```bash
qumo relay                # standalone / flat relay
qumo relay --role hub     # hub node — discovers no local peers
qumo relay --role edge    # edge node — discovers local hubs
```

See [Configuration]({{< relref "../configuration" >}}) for the environment
variables, and [Deployment → Peer topology]({{< relref "../deployment/peer-topology" >}})
for how `--role` fits into peer discovery.
