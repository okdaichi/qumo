package observability

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span represents a traced operation.
// It must be ended by calling End or Error.
type Span struct {
	ctx   context.Context
	span  trace.Span
	timer *prometheus.Timer
	onEnd func()
}

// Start begins a new span with the given name.
// The returned Span must be ended by calling End.
//
// Example:
//
//	ctx, span := observability.Start(ctx, "db.query")
//	defer span.End()
//
// With attributes:
//
//	ctx, span := observability.Start(ctx, "relay.serve",
//	    observability.Track("video"),
//	    observability.GroupSequence(42),
//	)
//	defer span.End()
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, *Span) {
	s := &Span{ctx: ctx}

	t := tracer()
	if t != nil {
		var opts []trace.SpanStartOption
		if len(attrs) > 0 {
			opts = append(opts, trace.WithAttributes(attrs...))
		}
		ctx, s.span = t.Start(ctx, name, opts...)
		s.ctx = ctx
	}

	return s.ctx, s
}

// StartWith begins a new span with options.
// Use this for advanced cases requiring latency metrics or lifecycle hooks.
//
// Example:
//
//	ctx, span := observability.StartWith(ctx, "relay.write",
//	    observability.Attrs(observability.Track("video")),
//	    observability.Latency(histogram),
//	    observability.OnEnd(cleanup),
//	)
//	defer span.End()
func StartWith(ctx context.Context, name string, opts ...SpanOpt) (context.Context, *Span) {
	s := &Span{ctx: ctx}

	var cfg spanOpts
	for _, opt := range opts {
		opt(&cfg)
	}

	t := tracer()
	if t != nil {
		var traceOpts []trace.SpanStartOption
		if len(cfg.attrs) > 0 {
			traceOpts = append(traceOpts, trace.WithAttributes(cfg.attrs...))
		}
		ctx, s.span = t.Start(ctx, name, traceOpts...)
		s.ctx = ctx
	}

	if cfg.latency != nil && MetricsEnabled() {
		s.timer = prometheus.NewTimer(cfg.latency)
	}

	if cfg.onStart != nil {
		cfg.onStart()
	}

	s.onEnd = cfg.onEnd

	return s.ctx, s
}

// End completes the span successfully.
func (s *Span) End() {
	if s.span != nil {
		s.span.SetStatus(codes.Ok, "")
		s.span.End()
	}
	if s.timer != nil {
		s.timer.ObserveDuration()
	}
	if s.onEnd != nil {
		s.onEnd()
	}
}

// Error records an error and ends the span.
// If err is nil, only the message is recorded.
func (s *Span) Error(err error, msg string) {
	if s.span != nil {
		if err != nil {
			s.span.RecordError(err)
		}
		s.span.SetStatus(codes.Error, msg)
		s.span.End()
	}
	if s.timer != nil {
		s.timer.ObserveDuration()
	}
	if s.onEnd != nil {
		s.onEnd()
	}
}

// Event adds an event to the span.
func (s *Span) Event(name string, attrs ...attribute.KeyValue) {
	if s.span == nil {
		return
	}
	if len(attrs) > 0 {
		s.span.AddEvent(name, trace.WithAttributes(attrs...))
	} else {
		s.span.AddEvent(name)
	}
}

// Set adds attributes to the span.
func (s *Span) Set(attrs ...attribute.KeyValue) {
	if s.span != nil {
		s.span.SetAttributes(attrs...)
	}
}

// Context returns the span's context.
func (s *Span) Context() context.Context {
	return s.ctx
}

// --- Span Options ---

// SpanOpt configures a span created with StartWith.
type SpanOpt func(*spanOpts)

type spanOpts struct {
	attrs   []attribute.KeyValue
	latency prometheus.Observer
	onStart func()
	onEnd   func()
}

// Attrs adds attributes to the span.
func Attrs(attrs ...attribute.KeyValue) SpanOpt {
	return func(o *spanOpts) {
		o.attrs = append(o.attrs, attrs...)
	}
}

// Latency records operation duration to the histogram.
func Latency(h prometheus.Observer) SpanOpt {
	return func(o *spanOpts) {
		o.latency = h
	}
}

// OnStart registers a function called when the span starts.
func OnStart(fn func()) SpanOpt {
	return func(o *spanOpts) {
		o.onStart = fn
	}
}

// OnEnd registers a function called when the span ends.
func OnEnd(fn func()) SpanOpt {
	return func(o *spanOpts) {
		o.onEnd = fn
	}
}
