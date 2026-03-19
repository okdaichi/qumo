package rtmp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

func ServerHandshake(rw io.ReadWriter) error {
	var c0 handshakeChunk0
	if err := c0.Decode(rw); err != nil {
		return fmt.Errorf("failed to read C0: %w", err)
	}

	var c1 handshakeChunk1
	if err := c1.Decode(rw); err != nil {
		return fmt.Errorf("failed to read C1: %w", err)
	}

	serverTime := uint32(time.Now().UnixMilli())

	s0 := handshakeChunk0{
		version: uint8(DefaultServerVersion),
	}

	s1 := handshakeChunk1{
		time: serverTime,
	}
	if _, err := rand.Read(s1.rand[:]); err != nil {
		return fmt.Errorf("failed to generate S1 random bytes: %w", err)
	}

	s2 := handshakeChunk2{
		receivedTimestamp: c1.time,
		readTime:          serverTime,
		echo:              c1.rand,
	}

	if err := s0.Encode(rw); err != nil {
		return fmt.Errorf("failed to write S0: %w", err)
	}
	if err := s1.Encode(rw); err != nil {
		return fmt.Errorf("failed to write S1: %w", err)
	}
	if err := s2.Encode(rw); err != nil {
		return fmt.Errorf("failed to write S2: %w", err)
	}

	var c2 handshakeChunk2
	if err := c2.Decode(rw); err != nil {
		return fmt.Errorf("failed to read C2: %w", err)
	}

	return nil
}

type handshakeChunk0 struct {
	version uint8
}

func (c handshakeChunk0) Encode(w io.Writer) error {
	_, err := w.Write([]byte{c.version})
	return err
}

func (c *handshakeChunk0) Decode(r io.Reader) error {
	buf := make([]byte, 1)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	c.version = buf[0]
	return nil
}

type handshakeChunk1 struct {
	time uint32
	rand [1528]byte
}

func (c handshakeChunk1) Encode(w io.Writer) error {
	buf := make([]byte, 1536)
	binary.BigEndian.PutUint32(buf[0:4], c.time)
	// bytes [4:8] are zero as per RTMP handshake spec.
	copy(buf[8:], c.rand[:])
	_, err := w.Write(buf)
	if err != nil {
		return err
	}
	return nil
}

func (c *handshakeChunk1) Decode(r io.Reader) error {
	buf := make([]byte, 1536)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	c.time = binary.BigEndian.Uint32(buf[0:4])
	copy(c.rand[:], buf[8:])
	return nil
}

type handshakeChunk2 struct {
	receivedTimestamp uint32
	readTime          uint32
	echo              [1528]byte
}

func (c handshakeChunk2) Encode(w io.Writer) error {
	buf := make([]byte, 1536)
	binary.BigEndian.PutUint32(buf[0:4], c.receivedTimestamp)
	binary.BigEndian.PutUint32(buf[4:8], c.readTime)
	copy(buf[8:], c.echo[:])
	_, err := w.Write(buf)
	if err != nil {
		return err
	}
	return nil
}

func (c *handshakeChunk2) Decode(r io.Reader) error {
	buf := make([]byte, 1536)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	c.receivedTimestamp = binary.BigEndian.Uint32(buf[0:4])
	c.readTime = binary.BigEndian.Uint32(buf[4:8])
	copy(c.echo[:], buf[8:])
	return nil
}

type Version uint8

const (
	Version3             Version = 3
	DefaultClientVersion         = Version3
	DefaultServerVersion         = Version3
)
