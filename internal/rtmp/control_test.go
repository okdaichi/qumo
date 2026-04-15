package rtmp

import (
	"bytes"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleControlMessage_Abort verifies that an Abort control message
// discards a partially received message on the specified chunk stream.
func TestHandleControlMessage_Abort(t *testing.T) {
	l := newConn(&fakeNetConn{})

	// Simulate partial message state on chunk stream 3.
	l.readStates[3] = &chunkReadState{
		payload:   make([]byte, 50),
		remaining: 100,
	}

	var buf bytes.Buffer
	(&messageAbort{ChunkStreamID: 3}).encode(&buf)
	msg := &rawMessage{
		typeID:  messageTypeAbort,
		payload: buf.Bytes(),
	}

	require.NoError(t, l.handleControlMessage(msg))
	assert.Nil(t, l.readStates[3].payload)
	assert.Equal(t, uint32(0), l.readStates[3].remaining)
}

// TestHandleControlMessage_SetChunkSize verifies chunk size is updated.
func TestHandleControlMessage_SetChunkSize(t *testing.T) {
	l := newConn(&fakeNetConn{})

	var buf bytes.Buffer
	(&messageSetChunkSize{ChunkSize: 512}).encode(&buf)
	msg := &rawMessage{
		typeID:  messageTypeSetChunkSize,
		payload: buf.Bytes(),
	}

	require.NoError(t, l.handleControlMessage(msg))
	assert.Equal(t, uint32(512), l.readChunkSize)
}

// TestHandleControlMessage_SetChunkSize_Invalid verifies that zero chunk size
// returns an error.
func TestHandleControlMessage_SetChunkSize_Invalid(t *testing.T) {
	l := newConn(&fakeNetConn{})

	var buf bytes.Buffer
	(&messageSetChunkSize{ChunkSize: 0}).encode(&buf)
	msg := &rawMessage{
		typeID:  messageTypeSetChunkSize,
		payload: buf.Bytes(),
	}

	assert.Error(t, l.handleControlMessage(msg))
}

// TestHandleControlMessage_Ack verifies peer ack bytes are tracked.
func TestHandleControlMessage_Ack(t *testing.T) {
	l := newConn(&fakeNetConn{})

	var buf bytes.Buffer
	(&messageAck{SequenceNumber: 500000}).encode(&buf)
	msg := &rawMessage{
		typeID:  messageTypeAck,
		payload: buf.Bytes(),
	}

	require.NoError(t, l.handleControlMessage(msg))
	assert.Equal(t, uint64(500000), l.peerAckedBytes)
}

// TestHandleControlMessage_WindowAckSize verifies window ack size is updated.
func TestHandleControlMessage_WindowAckSize(t *testing.T) {
	l := newConn(&fakeNetConn{})

	var buf bytes.Buffer
	(&messageWindowAckSize{Size: 5000000}).encode(&buf)
	msg := &rawMessage{
		typeID:  messageTypeWindowAckSize,
		payload: buf.Bytes(),
	}

	require.NoError(t, l.handleControlMessage(msg))
	assert.Equal(t, uint32(5000000), l.windowAckSize)
}

// TestHandleControlMessage_PingRequest verifies that a ping request triggers
// a ping response written to the transport.
func TestHandleControlMessage_PingRequest(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	l := newConn(client)

	var buf bytes.Buffer
	pingData := []byte{0x00, 0x01, 0x02, 0x03}
	(&messageUserControl{EventType: userControlPingRequest, EventData: pingData}).encode(&buf)
	msg := &rawMessage{
		typeID:  messageTypeUserControl,
		payload: buf.Bytes(),
	}

	// Read the response on server side.
	done := make(chan *rawMessage, 1)
	go func() {
		readerConn := newConn(server)
		e, _ := readerConn.readMessage()
		done <- e
	}()

	require.NoError(t, l.handleControlMessage(msg))

	resp := <-done
	require.NotNil(t, resp)
	assert.Equal(t, messageTypeUserControl, resp.typeID)

	// Decode the response.
	var uc messageUserControl
	require.NoError(t, uc.decode(bytes.NewReader(resp.payload)))
	assert.Equal(t, userControlPingResponse, uc.EventType)
	assert.Equal(t, pingData, uc.EventData)
}

// TestIsControlMessage verifies message type classification.
func TestIsControlMessage(t *testing.T) {
	tests := map[string]struct {
		typeID messageTypeID
		want   bool
	}{
		"SetChunkSize":     {typeID: messageTypeSetChunkSize, want: true},
		"Abort":            {typeID: messageTypeAbort, want: true},
		"Ack":              {typeID: messageTypeAck, want: true},
		"UserControl":      {typeID: messageTypeUserControl, want: true},
		"WindowAckSize":    {typeID: messageTypeWindowAckSize, want: true},
		"SetPeerBandwidth": {typeID: messageTypeSetPeerBandwidth, want: true},
		"Audio":            {typeID: messageTypeAudio, want: false},
		"Video":            {typeID: messageTypeVideo, want: false},
		"AMF0Command":      {typeID: messageTypeAMF0Command, want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, isControlMessage(tt.typeID))
		})
	}
}

// TestMessageWriter_WriteFrame exercises the MessageWriter frame transmission.
func TestMessageWriter_WriteFrame(t *testing.T) {
	tests := map[string]struct {
		frame    *Frame
		wantType messageTypeID
	}{
		"video frame": {
			frame:    &Frame{Type: FrameTypeVideo, Timestamp: 100, Data: []byte{0x17, 0x01}},
			wantType: messageTypeVideo,
		},
		"audio frame": {
			frame:    &Frame{Type: FrameTypeAudio, Timestamp: 200, Data: []byte{0xAF, 0x01}},
			wantType: messageTypeAudio,
		},
		"metadata frame": {
			frame:    &Frame{Type: FrameTypeMetadata, Timestamp: 0, Data: []byte{0x02, 0x00}},
			wantType: messageTypeAMF0Data,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()

			writerConn := newConn(client)
			readerConn := newConn(server)
			w := &MessageWriter{conn: writerConn, streamID: 1, app: "live", streamKey: "test"}

			errCh := make(chan error, 1)
			go func() {
				errCh <- w.WriteFrame(tt.frame)
			}()

			msg, err := readerConn.readMessage()
			require.NoError(t, err)
			require.NoError(t, <-errCh)

			assert.Equal(t, tt.wantType, msg.typeID)
			assert.Equal(t, messageStreamID(1), msg.streamID)
			assert.Equal(t, tt.frame.Timestamp, msg.timestamp)
			assert.Equal(t, tt.frame.Data, msg.payload)
		})
	}
}

// TestMessageWriter_WriteFrame_UnsupportedType verifies that writing an
// unsupported frame type returns an error.
func TestMessageWriter_WriteFrame_UnsupportedType(t *testing.T) {
	l := newConn(&fakeNetConn{})
	w := &MessageWriter{conn: l, streamID: 1}

	err := w.WriteFrame(&Frame{Type: FrameType(99), Data: []byte{0x01}})
	assert.Error(t, err)
}

// TestMessageReader_ReadFrame exercises frame delivery via MessageReader.
func TestMessageReader_ReadFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writerConn := newConn(client)
	readerConn := newConn(server)

	reader := &MessageReader{conn: readerConn, streamID: 1, app: "live", streamKey: "key"}

	// Write a video message on the writer side.
	videoData := []byte{0x17, 0x01, 0x00, 0x00, 0x00}
	errCh := make(chan error, 1)
	go func() {
		errCh <- writerConn.writeRawMessage(csidVideo, &rawMessage{
			typeID:    messageTypeVideo,
			streamID:  1,
			timestamp: 33,
			payload:   videoData,
		})
	}()

	frame, err := reader.ReadFrame()
	require.NoError(t, err)
	require.NoError(t, <-errCh)

	assert.Equal(t, FrameTypeVideo, frame.Type)
	assert.Equal(t, uint32(33), frame.Timestamp)
	assert.Equal(t, videoData, frame.Data)
}

// TestMessageReader_ReadFrame_SkipOtherStreams verifies frames on other
// stream IDs are skipped.
func TestMessageReader_ReadFrame_SkipOtherStreams(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writerConn := newConn(client)
	readerConn := newConn(server)

	reader := &MessageReader{conn: readerConn, streamID: 1, app: "live", streamKey: "key"}

	errCh := make(chan error, 1)
	go func() {
		// Send a message on stream 99 (should be skipped).
		if err := writerConn.writeRawMessage(csidVideo, &rawMessage{
			typeID:   messageTypeVideo,
			streamID: 99,
			payload:  []byte{0xFF},
		}); err != nil {
			errCh <- err
			return
		}
		// Send a message on stream 1 (should be returned).
		errCh <- writerConn.writeRawMessage(csidAudio, &rawMessage{
			typeID:    messageTypeAudio,
			streamID:  1,
			timestamp: 100,
			payload:   []byte{0xAF},
		})
	}()

	frame, err := reader.ReadFrame()
	require.NoError(t, err)
	require.NoError(t, <-errCh)

	assert.Equal(t, FrameTypeAudio, frame.Type)
	assert.Equal(t, uint32(100), frame.Timestamp)
}

// TestMessageReader_App_StreamKey verifies accessor methods.
func TestMessageReader_App_StreamKey(t *testing.T) {
	reader := &MessageReader{app: "live", streamKey: "mystream"}
	assert.Equal(t, "live", reader.App())
	assert.Equal(t, "mystream", reader.StreamKey())
}

// TestMessageWriter_App_StreamKey verifies accessor methods.
func TestMessageWriter_App_StreamKey(t *testing.T) {
	writer := &MessageWriter{app: "live", streamKey: "mystream"}
	assert.Equal(t, "live", writer.App())
	assert.Equal(t, "mystream", writer.StreamKey())
}
