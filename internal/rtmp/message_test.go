package rtmp

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageSetChunkSize_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		chunkSize uint32
	}{
		"default chunk size": {chunkSize: 128},
		"server chunk size":  {chunkSize: 4096},
		"max valid size":     {chunkSize: 0x7FFFFFFF},
		"minimum size":       {chunkSize: 1},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := messageSetChunkSize{ChunkSize: tt.chunkSize}
			require.NoError(t, orig.encode(&buf))

			var dec messageSetChunkSize
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.chunkSize, dec.ChunkSize)
		})
	}
}

func TestMessageSetChunkSize_HighBitCleared(t *testing.T) {
	// The high bit must always be 0 per the RTMP spec.
	var buf bytes.Buffer
	orig := messageSetChunkSize{ChunkSize: 0xFFFFFFFF}
	require.NoError(t, orig.encode(&buf))

	var dec messageSetChunkSize
	require.NoError(t, dec.decode(&buf))
	assert.Equal(t, uint32(0x7FFFFFFF), dec.ChunkSize)
}

func TestMessageAbort_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		csid uint32
	}{
		"control stream":  {csid: 2},
		"command stream":  {csid: 3},
		"large stream id": {csid: 65599},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := messageAbort{ChunkStreamID: tt.csid}
			require.NoError(t, orig.encode(&buf))

			var dec messageAbort
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.csid, dec.ChunkStreamID)
		})
	}
}

func TestMessageAck_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		seq uint32
	}{
		"zero":    {seq: 0},
		"typical": {seq: 2500000},
		"max":     {seq: 0xFFFFFFFF},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := messageAck{SequenceNumber: tt.seq}
			require.NoError(t, orig.encode(&buf))

			var dec messageAck
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.seq, dec.SequenceNumber)
		})
	}
}

func TestMessageUserControl_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		eventType uint16
		eventData []byte
	}{
		"stream begin": {
			eventType: userControlStreamBegin,
			eventData: []byte{0x00, 0x00, 0x00, 0x01},
		},
		"stream EOF": {
			eventType: userControlStreamEOF,
			eventData: []byte{0x00, 0x00, 0x00, 0x01},
		},
		"ping request": {
			eventType: userControlPingRequest,
			eventData: []byte{0x00, 0x01, 0x02, 0x03},
		},
		"empty event data": {
			eventType: 99,
			eventData: []byte{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := messageUserControl{EventType: tt.eventType, EventData: tt.eventData}
			require.NoError(t, orig.encode(&buf))

			var dec messageUserControl
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.eventType, dec.EventType)
			assert.Equal(t, tt.eventData, dec.EventData)
		})
	}
}

func TestMessageWindowAckSize_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		size uint32
	}{
		"default":  {size: 2500000},
		"zero":     {size: 0},
		"large":    {size: 10000000},
		"max uint": {size: 0xFFFFFFFF},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := messageWindowAckSize{Size: tt.size}
			require.NoError(t, orig.encode(&buf))

			var dec messageWindowAckSize
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.size, dec.Size)
		})
	}
}

func TestMessageSetPeerBandwidth_EncodeDecode(t *testing.T) {
	tests := map[string]struct {
		bandwidth uint32
		limitType bandwidthLimitType
	}{
		"hard limit": {
			bandwidth: 2500000,
			limitType: bandwidthLimitHard,
		},
		"soft limit": {
			bandwidth: 5000000,
			limitType: bandwidthLimitSoft,
		},
		"dynamic limit": {
			bandwidth: 2500000,
			limitType: bandwidthLimitDynamic,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := messageSetPeerBandwidth{Bandwidth: tt.bandwidth, LimitType: tt.limitType}
			require.NoError(t, orig.encode(&buf))

			var dec messageSetPeerBandwidth
			require.NoError(t, dec.decode(&buf))
			assert.Equal(t, tt.bandwidth, dec.Bandwidth)
			assert.Equal(t, tt.limitType, dec.LimitType)
		})
	}
}

func TestMessageDecode_ShortInput(t *testing.T) {
	tests := map[string]struct {
		decode func(r *bytes.Reader) error
	}{
		"SetChunkSize": {
			decode: func(r *bytes.Reader) error {
				var e messageSetChunkSize
				return e.decode(r)
			},
		},
		"Abort": {
			decode: func(r *bytes.Reader) error {
				var e messageAbort
				return e.decode(r)
			},
		},
		"Ack": {
			decode: func(r *bytes.Reader) error {
				var e messageAck
				return e.decode(r)
			},
		},
		"UserControl": {
			decode: func(r *bytes.Reader) error {
				var e messageUserControl
				return e.decode(r)
			},
		},
		"WindowAckSize": {
			decode: func(r *bytes.Reader) error {
				var e messageWindowAckSize
				return e.decode(r)
			},
		},
		"SetPeerBandwidth": {
			decode: func(r *bytes.Reader) error {
				var e messageSetPeerBandwidth
				return e.decode(r)
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := bytes.NewReader([]byte{0x00}) // too short for any message
			err := tt.decode(r)
			assert.Error(t, err)
		})
	}
}
