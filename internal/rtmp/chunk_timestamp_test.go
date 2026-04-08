package rtmp

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeChunkTimestamp(t *testing.T) {
	var buf bytes.Buffer
	const normalTimestamp = uint32(0x112233)
	if err := encodeChunkTimestamp(&buf, normalTimestamp); err != nil {
		t.Fatalf("encodeChunkTimestamp failed: %v", err)
	}
	if got, err := decodeChunkTimestamp(normalTimestamp, &buf); err != nil {
		t.Fatalf("decodeChunkTimestamp failed: %v", err)
	} else if got != normalTimestamp {
		t.Fatalf("decodeChunkTimestamp got=%#x want=%#x", got, normalTimestamp)
	}

	buf.Reset()
	const extendedTimestamp = uint32(0x01020304)
	if err := encodeChunkTimestamp(&buf, extendedTimestamp); err != nil {
		t.Fatalf("encodeChunkTimestamp failed for extended timestamp: %v", err)
	}
	if got, err := decodeChunkTimestamp(chunkTimestampMax, &buf); err != nil {
		t.Fatalf("decodeChunkTimestamp failed for extended timestamp: %v", err)
	} else if got != extendedTimestamp {
		t.Fatalf("decodeChunkTimestamp got=%#x want=%#x", got, extendedTimestamp)
	}
}
