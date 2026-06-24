package rtsp

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// mockConn implements net.Conn for testing purposes.
type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
}

func newMockConn() *mockConn {
	return &mockConn{
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.EOF
	}
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.ErrClosedPipe
	}
	return m.writeBuf.Write(b)
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5678}
}

func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestConn_ReadRequest(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantReq   *Request
		wantFrame *InterleavedFrame
		wantErr   bool
	}{
		{
			name:  "standard request",
			input: "OPTIONS rtsp://example.com/media.mp4 RTSP/1.0\r\nCSeq: 1\r\n\r\n",
			wantReq: &Request{
				Method: MethodOptions,
				URL: &url.URL{
					Scheme: "rtsp",
					Host:   "example.com",
					Path:   "/media.mp4",
				},
				Proto: "RTSP/1.0",
				Header: http.Header{
					"Cseq": []string{"1"},
				},
			},
			wantFrame: nil,
			wantErr:   false,
		},
		{
			name:    "interleaved frame",
			input:   string([]byte{'$', 0, 0, 4, 'a', 'b', 'c', 'd'}),
			wantReq: nil,
			wantFrame: &InterleavedFrame{
				Channel: 0,
				Payload: []byte{'a', 'b', 'c', 'd'},
			},
			wantErr: false,
		},
		{
			name:      "malformed interleaved frame",
			input:     string([]byte{'$', 0, 0, 4, 'a'}), // Incomplete payload
			wantReq:   nil,
			wantFrame: nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockConn()
			mc.readBuf.WriteString(tt.input)
			conn := newConn(mc)

			req, frame, err := conn.ReadRequest()

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantReq != nil {
				if req == nil {
					t.Fatalf("expected request, got nil")
				}
				if req.Method != tt.wantReq.Method {
					t.Errorf("Method = %v, want %v", req.Method, tt.wantReq.Method)
				}
				if req.URL.String() != tt.wantReq.URL.String() {
					t.Errorf("URL = %v, want %v", req.URL.String(), tt.wantReq.URL.String())
				}
				if req.Proto != tt.wantReq.Proto {
					t.Errorf("Proto = %v, want %v", req.Proto, tt.wantReq.Proto)
				}
				if !reflect.DeepEqual(req.Header, tt.wantReq.Header) {
					t.Errorf("Header = %v, want %v", req.Header, tt.wantReq.Header)
				}
			}

			if tt.wantFrame != nil {
				if frame == nil {
					t.Fatalf("expected frame, got nil")
				}
				if !reflect.DeepEqual(frame, tt.wantFrame) {
					t.Errorf("Frame = %v, want %v", frame, tt.wantFrame)
				}
			}
		})
	}
}

func TestConn_ReadResponse(t *testing.T) {
	req := &Request{
		Method: MethodOptions,
		URL: &url.URL{
			Scheme: "rtsp",
			Host:   "example.com",
			Path:   "/media.mp4",
		},
		Proto: "RTSP/1.0",
		Header: http.Header{
			"Cseq": []string{"1"},
		},
	}

	tests := []struct {
		name      string
		input     string
		wantResp  *Response
		wantFrame *InterleavedFrame
		wantErr   bool
	}{
		{
			name:  "standard response",
			input: "RTSP/1.0 200 OK\r\nCSeq: 1\r\n\r\n",
			wantResp: &Response{
				Proto:      "RTSP/1.0",
				StatusCode: 200,
				Status:     "200 OK",
				Header: http.Header{
					"Cseq": []string{"1"},
				},
				Request: req,
			},
			wantFrame: nil,
			wantErr:   false,
		},
		{
			name:     "interleaved frame",
			input:    string([]byte{'$', 1, 0, 2, 'o', 'k'}),
			wantResp: nil,
			wantFrame: &InterleavedFrame{
				Channel: 1,
				Payload: []byte{'o', 'k'},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockConn()
			mc.readBuf.WriteString(tt.input)
			conn := newConn(mc)

			resp, frame, err := conn.ReadResponse(req)

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantResp != nil {
				if resp == nil {
					t.Fatalf("expected response, got nil")
				}
				if resp.Proto != tt.wantResp.Proto {
					t.Errorf("Proto = %v, want %v", resp.Proto, tt.wantResp.Proto)
				}
				if resp.StatusCode != tt.wantResp.StatusCode {
					t.Errorf("StatusCode = %v, want %v", resp.StatusCode, tt.wantResp.StatusCode)
				}
				if resp.Status != tt.wantResp.Status {
					t.Errorf("Status = %v, want %v", resp.Status, tt.wantResp.Status)
				}
				if !reflect.DeepEqual(resp.Header, tt.wantResp.Header) {
					t.Errorf("Header = %v, want %v", resp.Header, tt.wantResp.Header)
				}
			}

			if tt.wantFrame != nil {
				if frame == nil {
					t.Fatalf("expected frame, got nil")
				}
				if !reflect.DeepEqual(frame, tt.wantFrame) {
					t.Errorf("Frame = %v, want %v", frame, tt.wantFrame)
				}
			}
		})
	}
}

func TestConn_WriteRequest(t *testing.T) {
	mc := newMockConn()
	conn := newConn(mc)

	req := &Request{
		Method: MethodPlay,
		URL: &url.URL{
			Scheme: "rtsp",
			Host:   "example.com",
			Path:   "/stream",
		},
		Proto: "RTSP/1.0",
		Header: http.Header{
			"CSeq":    []string{"2"},
			"Session": []string{"12345678"},
		},
	}

	err := conn.WriteRequest(req)
	if err != nil {
		t.Fatalf("WriteRequest() error = %v", err)
	}

	out := mc.writeBuf.String()
	expectedPrefix := "PLAY rtsp://example.com/stream RTSP/1.0\r\n"
	if !strings.HasPrefix(out, expectedPrefix) {
		t.Errorf("output does not start with expected line, got: %q", out)
	}
}

func TestConn_WriteResponse(t *testing.T) {
	mc := newMockConn()
	conn := newConn(mc)

	resp := &Response{
		Proto:      "RTSP/1.0",
		StatusCode: 200,
		Status:     "200 OK",
		Header: http.Header{
			"CSeq": []string{"3"},
		},
	}

	err := conn.WriteResponse(resp)
	if err != nil {
		t.Fatalf("WriteResponse() error = %v", err)
	}

	out := mc.writeBuf.String()
	expectedPrefix := "RTSP/1.0 200 OK\r\n"
	if !strings.HasPrefix(out, expectedPrefix) {
		t.Errorf("output does not start with expected line, got: %q", out)
	}
}

func TestConn_WriteInterleavedFrame(t *testing.T) {
	mc := newMockConn()
	conn := newConn(mc)

	frame := &InterleavedFrame{
		Channel: 2,
		Payload: []byte{0x00, 0x01, 0x02, 0x03},
	}

	err := conn.WriteInterleavedFrame(frame)
	if err != nil {
		t.Fatalf("WriteInterleavedFrame() error = %v", err)
	}

	expected := []byte{'$', 2, 0, 4, 0x00, 0x01, 0x02, 0x03}
	if !bytes.Equal(mc.writeBuf.Bytes(), expected) {
		t.Errorf("WriteInterleavedFrame() output = %v, want %v", mc.writeBuf.Bytes(), expected)
	}
}

func TestConn_CloseAndRemoteAddr(t *testing.T) {
	mc := newMockConn()
	conn := newConn(mc)

	addr := conn.RemoteAddr()
	if addr == nil {
		t.Fatal("RemoteAddr() returned nil")
	}
	if addr.String() != "127.0.0.1:5678" {
		t.Errorf("RemoteAddr() = %v, want %v", addr.String(), "127.0.0.1:5678")
	}

	err := conn.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !mc.closed {
		t.Error("expected connection to be closed")
	}
}
