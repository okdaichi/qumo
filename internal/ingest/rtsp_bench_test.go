package ingest

import (
	"bytes"
	"io"
	"testing"
)

// BenchmarkReadAllBounded/Unbounded characterize the cost of bounding an RTSP
// ANNOUNCE body read with io.LimitReader vs an unbounded io.ReadAll. (#145 is a
// memory-safety bound rather than a speedup, so this measures the bounding cost,
// not a base-vs-PR speed delta.)
func BenchmarkReadAllUnbounded(b *testing.B) {
	payload := bytes.Repeat([]byte("a"), 1024*1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(payload)
		_, _ = io.ReadAll(r)
	}
}

func BenchmarkReadAllBounded(b *testing.B) {
	payload := bytes.Repeat([]byte("a"), 1024*1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(payload)
		_, _ = io.ReadAll(io.LimitReader(r, 64*1024))
	}
}
