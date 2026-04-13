package rtmp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// serverHandshake performs the server-side RTMP handshake (version negotiation
// and key exchange) over rw. It reads C0+C1 from the client, writes S0+S1+S2,
// and reads C2.
func serverHandshake(rw io.ReadWriter) error {
	var c0 handshakeChunk0
	if err := c0.decode(rw); err != nil {
		return fmt.Errorf("failed to read C0: %w", err)
	}

	var c1 handshakeChunk1
	if err := c1.decode(rw); err != nil {
		return fmt.Errorf("failed to read C1: %w", err)
	}

	serverTime := uint32(time.Now().UnixMilli())

	s0 := handshakeChunk0{
		version: uint8(defaultServerVersion),
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

	if err := s0.encode(rw); err != nil {
		return fmt.Errorf("failed to write S0: %w", err)
	}
	if err := s1.encode(rw); err != nil {
		return fmt.Errorf("failed to write S1: %w", err)
	}
	if err := s2.encode(rw); err != nil {
		return fmt.Errorf("failed to write S2: %w", err)
	}

	var c2 handshakeChunk2
	if err := c2.decode(rw); err != nil {
		return fmt.Errorf("failed to read C2: %w", err)
	}

	return nil
}

// clientHandshake performs the client-side RTMP handshake over rw. It writes
// C0+C1, reads S0+S1+S2 from the server, and writes C2.
func clientHandshake(rw io.ReadWriter) error {
	clientTime := uint32(time.Now().UnixMilli())

	c0 := handshakeChunk0{
		version: uint8(defaultClientVersion),
	}

	c1 := handshakeChunk1{
		time: clientTime,
	}
	if _, err := rand.Read(c1.rand[:]); err != nil {
		return fmt.Errorf("failed to generate C1 random bytes: %w", err)
	}

	if err := c0.encode(rw); err != nil {
		return fmt.Errorf("failed to write C0: %w", err)
	}
	if err := c1.encode(rw); err != nil {
		return fmt.Errorf("failed to write C1: %w", err)
	}

	var s0 handshakeChunk0
	if err := s0.decode(rw); err != nil {
		return fmt.Errorf("failed to read S0: %w", err)
	}

	var s1 handshakeChunk1
	if err := s1.decode(rw); err != nil {
		return fmt.Errorf("failed to read S1: %w", err)
	}

	var s2 handshakeChunk2
	if err := s2.decode(rw); err != nil {
		return fmt.Errorf("failed to read S2: %w", err)
	}

	c2 := handshakeChunk2{
		receivedTimestamp: s1.time,
		readTime:          uint32(time.Now().UnixMilli()),
		echo:              s1.rand,
	}
	if err := c2.encode(rw); err != nil {
		return fmt.Errorf("failed to write C2: %w", err)
	}

	return nil
}

type handshakeChunk0 struct {
	version uint8
}

func (c handshakeChunk0) encode(w io.Writer) error {
	_, err := w.Write([]byte{c.version})
	return err
}

func (c *handshakeChunk0) decode(r io.Reader) error {
	var buf [1]byte
	_, err := io.ReadFull(r, buf[:])
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

func (c handshakeChunk1) encode(w io.Writer) error {
	buf := make([]byte, 1536)
	binary.BigEndian.PutUint32(buf[0:4], c.time)
	// bytes [4:8] are zero as per RTMP handshake spec.
	copy(buf[8:], c.rand[:])
	_, err := w.Write(buf)
	return err
}

func (c *handshakeChunk1) decode(r io.Reader) error {
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

func (c handshakeChunk2) encode(w io.Writer) error {
	buf := make([]byte, 1536)
	binary.BigEndian.PutUint32(buf[0:4], c.receivedTimestamp)
	binary.BigEndian.PutUint32(buf[4:8], c.readTime)
	copy(buf[8:], c.echo[:])
	_, err := w.Write(buf)
	return err
}

func (c *handshakeChunk2) decode(r io.Reader) error {
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

// version represents an RTMP protocol version number exchanged during the
// handshake (C0/S0).
type version uint8

const (
	// version3 is the only version defined by the RTMP specification.
	version3 version = 3
	// defaultClientVersion is the version sent by the client during handshake.
	defaultClientVersion = version3
	// defaultServerVersion is the version sent by the server during handshake.
	defaultServerVersion = version3
)
