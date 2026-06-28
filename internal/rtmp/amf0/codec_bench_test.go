package amf0

import "testing"

// BenchmarkMarshalUnmarshal exercises the byte/uint16/uint32 I/O helpers on the
// hot encode/decode path (writeByte/readByte, writeU16/readU16, writeU32/readU32).
// It covers the amf0 readByte fast-path and the stack-buffer allocation changes.
func BenchmarkMarshalUnmarshal(b *testing.B) {
	val := map[string]any{
		"stream":   "live",
		"duration": float64(123.45),
		"active":   true,
	}
	data, err := Marshal(val)
	if err != nil {
		b.Fatalf("Marshal: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Marshal(val)
		_, _ = Unmarshal(data)
	}
}
