---
title: qumo
layout: hextra-home
description: A high-performance Media over QUIC (MoQ) relay server with peer-based content discovery, enabling distributed media streaming over the QUIC transport protocol.
---

{{<hextra/hero-badge>}}
<div class="hx:w-2 hx:h-2 hx:rounded-full hx:bg-primary-400"></div>
	<span>Apache 2.0, open source</span>
	{{<icon name="arrow-circle-right" attributes="height=14">}}
{{</hextra/hero-badge>}}

<div class="hx:mt-6 hx:mb-6">
{{<hextra/hero-headline>}}
A MoQ relay
&nbsp;<br class="hx:sm:block hx:hidden" />
built to fan out
{{</hextra/hero-headline>}}
</div>

<div class="hx:mb-12">
{{<hextra/hero-subtitle>}}
High-performance Media over QUIC relay server with
&nbsp;<br class="hx:sm:block hx:hidden" />
peer-based content discovery, for distributed real-time streaming.
{{</hextra/hero-subtitle>}}
</div>

<div class="hx:mb-12 hero-btn--green">
{{<hextra/hero-button text="Get Started" link="docs">}}
</div>

{{<hextra/feature-grid>}}
	{{<hextra/feature-card
		title="One binary, one command"
		subtitle="qumo playground boots an in-process relay plus embedded web UI at http://127.0.0.1:8080 — no config file required."
		icon="lightning-bolt"
	>}}
	{{<hextra/feature-card
		title="Peer-based topology"
		subtitle="Relays mesh via ANNOUNCE_PLEASE for decentralized content discovery — static peers, Nomad-native discovery, or a remote resolver."
		icon="globe-alt"
	>}}
	{{<hextra/feature-card
		title="MoQT protocol"
		subtitle="Full Media over QUIC Transport support (moq-lite draft-04), built on the gomoqt library."
		icon="document-text"
	>}}
	{{<hextra/feature-card
		title="Env-var zero-config"
		subtitle="No config file — every setting is an environment variable. Prebuilt multi-arch images on GHCR."
		icon="adjustments"
	>}}
	{{<hextra/feature-card
		title="Built-in observability"
		subtitle="Prometheus metrics, a health probe, opt-in pprof, and a qumo doctor command that explains effective runtime config."
		icon="chart-bar"
	>}}
	{{<hextra/feature-card
		title="RTMP & RTSP ingest"
		subtitle="Bridge existing RTMP encoders and RTSP/IP cameras into MoQT without touching the relay's peer mesh."
		icon="video-camera"
	>}}
{{</hextra/feature-grid>}}
