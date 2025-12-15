package observability

import "go.opentelemetry.io/otel/attribute"

// Attribute keys.
// MoQ-specific keys use the "moq." prefix.
const (
	keyTrack       = "moq.track"
	keyGroup       = "moq.group"
	keyFrames      = "moq.frames"
	keyBroadcast   = "moq.broadcast"
	keySubscribers = "moq.subscribers"
)

// --- Attribute Constructors ---

// Track returns a track name attribute.
func Track(name string) attribute.KeyValue {
	return attribute.String(keyTrack, name)
}

// Group returns a group sequence attribute.
func Group(seq uint64) attribute.KeyValue {
	return attribute.Int64(keyGroup, int64(seq))
}

// GroupSequence is an alias for Group.
func GroupSequence(seq uint64) attribute.KeyValue {
	return Group(seq)
}

// Frames returns a frame count attribute.
func Frames(n int) attribute.KeyValue {
	return attribute.Int(keyFrames, n)
}

// Broadcast returns a broadcast path attribute.
func Broadcast(path string) attribute.KeyValue {
	return attribute.String(keyBroadcast, path)
}

// Subscribers returns a subscriber count attribute.
func Subscribers(n int) attribute.KeyValue {
	return attribute.Int(keySubscribers, n)
}

// --- Generic Attribute Constructors ---

// Str creates a string attribute with custom key.
func Str(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

// Num creates an int64 attribute with custom key.
func Num(key string, value int64) attribute.KeyValue {
	return attribute.Int64(key, value)
}
