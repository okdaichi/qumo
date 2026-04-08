package rtmp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSendMessageStreamWriteMessage(t *testing.T) {
	stream := newSendMessageStream(1)
	msg := acquireMessage()
	msg.timestamp = 0x01020304
	msg.messageStreamID = 1
	msg.messageTypeID = byte(MessageTypeAudio)
	if _, err := msg.payload.Write([]byte{0xAB}); err != nil {
		t.Fatalf("payload write failed: %v", err)
	}
	if !stream.Enqueue(msg) {
		t.Fatal("enqueue failed")
	}

	var out bytes.Buffer
	if err := stream.WriteMessage(&out, chunkStreamIDControl, 1); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	got := out.Bytes()
	want := make([]byte, 0, 1+11+4+1)
	want = append(want, 0x02)
	want = append(want, 0xFF, 0xFF, 0xFF)
	want = append(want, 0x00, 0x00, 0x01)
	want = append(want, byte(MessageTypeAudio))
	var streamID [4]byte
	binary.LittleEndian.PutUint32(streamID[:], 1)
	want = append(want, streamID[:]...)
	var ext [4]byte
	binary.BigEndian.PutUint32(ext[:], 0x01020304)
	want = append(want, ext[:]...)
	want = append(want, 0xAB)

	if !bytes.Equal(got, want) {
		t.Fatalf("WriteMessage mismatch:\n got=%v\nwant=%v", got, want)
	}
}
