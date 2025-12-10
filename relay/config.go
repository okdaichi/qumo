package relay

type Config struct {
	FrameCapacity int

	// MaxGroupCache is the maximum number of group caches to keep.
	// If zero, it means no group caches are kept.
	MaxGroupCache int
}
