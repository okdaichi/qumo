package rtmp

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkBasicHeader_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		fmt  uint8
		csid chunkStreamID
	}{
		"1 byte: csid 2":    {fmt: 0, csid: 2},
		"1 byte: csid 63":   {fmt: 1, csid: 63},
		"2 bytes: csid 64":  {fmt: 2, csid: 64},
		"2 bytes: csid 319": {fmt: 3, csid: 319},
		"3 bytes: csid 320": {fmt: 0, csid: 320},
		"3 bytes: csid 65599": {fmt: 1, csid: 65599},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := chunkBasicHeader{fmt: tt.fmt, chunkStreamID: tt.csid}
			require.NoError(t, orig.encode(&buf))

			var dec chunkBasicHeader
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.fmt, dec.fmt)
			assert.Equal(t, tt.csid, dec.chunkStreamID)
		})
	}
}

func TestChunkBasicHeader_Decode_Errors(t *testing.T) {
	tests := map[string]struct {
		input []byte
	}{
		"empty": {
			input: []byte{},
		},
		"missing byte 2": {
			input: []byte{0x00}, // csidPart == 0 expects 2 bytes total
		},
		"missing byte 2 and 3": {
			input: []byte{0x01}, // csidPart == 1 expects 3 bytes total
		},
		"missing byte 3": {
			input: []byte{0x01, 0x00}, // csidPart == 1 expects 3 bytes total
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := bytes.NewReader(tt.input)
			var dec chunkBasicHeader
			err := dec.decode(r)
			assert.Error(t, err)
		})
	}
}

func TestExtendedTimestamp_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		ts uint32
	}{
		"zero": {ts: 0},
		"typical": {ts: 123456789},
		"max uint32": {ts: 0xFFFFFFFF},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, encodeExtendedTimestamp(&buf, tt.ts))

			dec, err := decodeExtendedTimestamp(&buf)
			require.NoError(t, err)
			assert.Equal(t, tt.ts, dec)
		})
	}
}

func TestExtendedTimestamp_Decode_Error(t *testing.T) {
	tests := map[string]struct {
		input []byte
	}{
		"empty": {input: []byte{}},
		"1 byte": {input: []byte{0x01}},
		"3 bytes": {input: []byte{0x01, 0x02, 0x03}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := bytes.NewReader(tt.input)
			_, err := decodeExtendedTimestamp(r)
			assert.Error(t, err)
		})
	}
}
