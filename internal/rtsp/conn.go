package rtsp

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

type writeTask struct {
	req   *Request
	resp  *Response
	frame *InterleavedFrame
	errCh chan error
}

// Conn represents an RTSP connection.
type Conn struct {
	transport net.Conn
	br        *bufio.Reader
	bw        *bufio.Writer

	writeChan chan writeTask
	closeChan chan struct{}
	closeOnce sync.Once
}

func newConn(transport net.Conn) *Conn {
	c := &Conn{
		transport: transport,
		br:        bufio.NewReader(transport),
		bw:        bufio.NewWriter(transport),
		writeChan: make(chan writeTask, 128),
		closeChan: make(chan struct{}),
	}
	go c.writerLoop()
	return c
}

func (c *Conn) writerLoop() {
	defer c.Close()
	for {
		select {
		case <-c.closeChan:
			return
		case task := <-c.writeChan:
			var err error
			if task.frame != nil {
				var header [4]byte
				header[0] = '$'
				header[1] = task.frame.Channel
				binary.BigEndian.PutUint16(header[2:], uint16(len(task.frame.Payload)))

				if _, err = c.bw.Write(header[:]); err == nil {
					if _, err = c.bw.Write(task.frame.Payload); err == nil {
						err = c.bw.Flush()
					}
				}
			} else if task.req != nil {
				if err = task.req.Write(c.bw); err == nil {
					err = c.bw.Flush()
				}
			} else if task.resp != nil {
				if err = task.resp.Write(c.bw); err == nil {
					err = c.bw.Flush()
				}
			}

			if task.errCh != nil {
				task.errCh <- err
			}

			if err != nil {
				return
			}
		}
	}
}

// ReadRequest reads an RTSP request or an interleaved frame.
func (c *Conn) ReadRequest() (*Request, *InterleavedFrame, error) {
	b, err := c.br.Peek(1)
	if err != nil {
		return nil, nil, err
	}

	if b[0] == '$' {
		frame, err := c.readInterleavedFrame()
		return nil, frame, err
	}

	req, err := ReadRequest(c.br)
	return req, nil, err
}

// ReadResponse reads an RTSP response or an interleaved frame.
func (c *Conn) ReadResponse(req *Request) (*Response, *InterleavedFrame, error) {
	b, err := c.br.Peek(1)
	if err != nil {
		return nil, nil, err
	}

	if b[0] == '$' {
		frame, err := c.readInterleavedFrame()
		return nil, frame, err
	}

	resp, err := ReadResponse(c.br, req)
	return resp, nil, err
}

func (c *Conn) readInterleavedFrame() (*InterleavedFrame, error) {
	var header [4]byte
	if _, err := io.ReadFull(c.br, header[:]); err != nil {
		return nil, err
	}

	if header[0] != '$' {
		return nil, fmt.Errorf("malformed interleaved frame header")
	}

	channel := header[1]
	length := binary.BigEndian.Uint16(header[2:])

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return nil, err
	}

	return &InterleavedFrame{
		Channel: channel,
		Payload: payload,
	}, nil
}

// WriteRequest writes an RTSP request to the connection.
func (c *Conn) WriteRequest(req *Request) error {
	errCh := make(chan error, 1)
	select {
	case <-c.closeChan:
		return net.ErrClosed
	case c.writeChan <- writeTask{req: req, errCh: errCh}:
		select {
		case <-c.closeChan:
			return net.ErrClosed
		case err := <-errCh:
			return err
		}
	}
}

// WriteResponse writes an RTSP response to the connection.
func (c *Conn) WriteResponse(resp *Response) error {
	errCh := make(chan error, 1)
	select {
	case <-c.closeChan:
		return net.ErrClosed
	case c.writeChan <- writeTask{resp: resp, errCh: errCh}:
		select {
		case <-c.closeChan:
			return net.ErrClosed
		case err := <-errCh:
			return err
		}
	}
}

// WriteInterleavedFrame writes an interleaved frame to the connection.
func (c *Conn) WriteInterleavedFrame(frame *InterleavedFrame) error {
	errCh := make(chan error, 1)
	select {
	case <-c.closeChan:
		return net.ErrClosed
	case c.writeChan <- writeTask{frame: frame, errCh: errCh}:
		select {
		case <-c.closeChan:
			return net.ErrClosed
		case err := <-errCh:
			return err
		}
	}
}

// Close closes the connection.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeChan)
		c.transport.Close()
	})
	return nil
}

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr {
	return c.transport.RemoteAddr()
}
