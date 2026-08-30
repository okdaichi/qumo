package amf3

import (
	"bytes"
	"fmt"
	"testing"
)

// BenchmarkReadByte targets the readByte fast path (io.ByteReader) that the
// decoder hits on its buffered reader.
func BenchmarkReadByte(b *testing.B) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	r := bytes.NewReader(data)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = readByte(r)
		if r.Len() == 0 {
			r.Reset(data)
		}
	}
}

// BenchmarkSortedKeys targets the map-key collection used when encoding
// associative amf3 arrays and dynamic/sealed objects.
func BenchmarkSortedKeys(b *testing.B) {
	m := make(map[string]int, 32)
	for i := range 32 {
		m[fmt.Sprintf("key-%02d", i)] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sortedKeys(m)
	}
}
