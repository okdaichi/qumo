package rtmp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSendChunkStreamWriteMessageWithExtendedTimestamp(t *testing.T) {
	stream := newSendChunkStream(chunkStreamIDControl, 0x01020304, 1, byte(MessageTypeAudio), 1)
	stream.payload = bytes.NewBuffer([]byte{0xAB})
	stream.chunkBuf = make([]byte, 1)

	var out bytes.Buffer
	if err := stream.writeMessage(&out); err != nil {
		t.Fatalf("writeMessage failed: %v", err)
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
		t.Fatalf("writeMessage mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestSendChunkStreamWriteMessageWithContinuationAndExtendedTimestamp(t *testing.T) {
	stream := newSendChunkStream(chunkStreamIDControl, 0x01020304, 2, byte(MessageTypeAudio), 1)
	stream.payload = bytes.NewBuffer([]byte{0xAB, 0xCD})
	stream.chunkBuf = make([]byte, 1)

	var out bytes.Buffer
	if err := stream.writeMessage(&out); err != nil {
		t.Fatalf("writeMessage failed: %v", err)
	}

	got := out.Bytes()
	want := make([]byte, 0, 2*(1+4)+1+11+4)
	want = append(want, 0x02)
	want = append(want, 0xFF, 0xFF, 0xFF)
	want = append(want, 0x00, 0x00, 0x02)
	want = append(want, byte(MessageTypeAudio))
	var streamID [4]byte
	binary.LittleEndian.PutUint32(streamID[:], 1)
	want = append(want, streamID[:]...)
	var ext [4]byte
	binary.BigEndian.PutUint32(ext[:], 0x01020304)
	want = append(want, ext[:]...)
	want = append(want, 0xAB)
	want = append(want, 0xC2)
	want = append(want, ext[:]...)
	want = append(want, 0xCD)

	if !bytes.Equal(got, want) {
		t.Fatalf("writeMessage mismatch:\n got=%v\nwant=%v", got, want)
	}
}
