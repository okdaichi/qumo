package rtmp

import (
	"bytes"
	"io"
	"testing"
)

func TestChunk0EncodeDecode(t *testing.T) {
	original := handshakeChunk0{version: uint8(DefaultClientVersion)}

	var buf bytes.Buffer
	if err := original.encode(&buf); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	var decoded handshakeChunk0
	if err := decoded.decode(&buf); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decoded.version != original.version {
		t.Fatalf("version mismatch: got=%d want=%d", decoded.version, original.version)
	}
}

func TestChunkC1EncodeDecode(t *testing.T) {
	var rnd [1528]byte
	for i := range rnd {
		rnd[i] = byte(i % 251)
	}

	original := handshakeChunk1{
		time: 0x01020304,
		rand: rnd,
	}

	var buf bytes.Buffer
	if err := original.encode(&buf); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	if got, want := buf.Len(), 1536; got != want {
		t.Fatalf("encoded length mismatch: got=%d want=%d", got, want)
	}

	var decoded handshakeChunk1
	if err := decoded.decode(&buf); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decoded.time != original.time {
		t.Fatalf("time mismatch: got=%d want=%d", decoded.time, original.time)
	}
	if decoded.rand != original.rand {
		t.Fatalf("rand mismatch")
	}
}

func TestChunk2EncodeDecode(t *testing.T) {
	var echo [1528]byte
	for i := range echo {
		echo[i] = byte((i * 3) % 251)
	}

	original := handshakeChunk2{
		receivedTimestamp: 0x11223344,
		readTime:          0x55667788,
		echo:              echo,
	}

	var buf bytes.Buffer
	if err := original.encode(&buf); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	if got, want := buf.Len(), 1536; got != want {
		t.Fatalf("encoded length mismatch: got=%d want=%d", got, want)
	}

	var decoded handshakeChunk2
	if err := decoded.decode(&buf); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decoded.receivedTimestamp != original.receivedTimestamp {
		t.Fatalf("receivedTimestamp mismatch: got=%d want=%d", decoded.receivedTimestamp, original.receivedTimestamp)
	}
	if decoded.readTime != original.readTime {
		t.Fatalf("readTime mismatch: got=%d want=%d", decoded.readTime, original.readTime)
	}
	if decoded.echo != original.echo {
		t.Fatalf("echo mismatch")
	}
}

func TestChunkDecodeShortRead(t *testing.T) {
	t.Run("chunk0", func(t *testing.T) {
		var c handshakeChunk0
		err := c.decode(bytes.NewReader(nil))
		if err == nil {
			t.Fatal("expected error for short read")
		}
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("chunkC1", func(t *testing.T) {
		var c handshakeChunk1
		err := c.decode(bytes.NewReader(make([]byte, 100)))
		if err == nil {
			t.Fatal("expected error for short read")
		}
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("chunk2", func(t *testing.T) {
		var c handshakeChunk2
		err := c.decode(bytes.NewReader(make([]byte, 100)))
		if err == nil {
			t.Fatal("expected error for short read")
		}
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
