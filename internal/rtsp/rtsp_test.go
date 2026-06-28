package rtsp

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestReadRequest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*testing.T, *Request)
	}{
		{
			name:    "valid without body",
			input:   "OPTIONS rtsp://example.com/media.mp4 RTSP/1.0\r\nCSeq: 1\r\n\r\n",
			wantErr: false,
			check: func(t *testing.T, req *Request) {
				if req.Method != MethodOptions {
					t.Errorf("expected MethodOptions, got %q", req.Method)
				}
				if req.URL.String() != "rtsp://example.com/media.mp4" {
					t.Errorf("expected rtsp://example.com/media.mp4, got %q", req.URL.String())
				}
				if req.Proto != "RTSP/1.0" {
					t.Errorf("expected RTSP/1.0, got %q", req.Proto)
				}
				if req.Header.Get("CSeq") != "1" {
					t.Errorf("expected CSeq 1, got %q", req.Header.Get("CSeq"))
				}
				if req.Body != nil {
					t.Errorf("expected nil body")
				}
			},
		},
		{
			name:    "valid with body",
			input:   "ANNOUNCE rtsp://example.com/media.mp4 RTSP/1.0\r\nCSeq: 1\r\nContent-Length: 4\r\n\r\ntest",
			wantErr: false,
			check: func(t *testing.T, req *Request) {
				if req.Method != MethodAnnounce {
					t.Errorf("expected MethodAnnounce, got %q", req.Method)
				}
				if req.Body == nil {
					t.Fatalf("expected non-nil body")
				}
				b, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("failed to read body: %v", err)
				}
				if string(b) != "test" {
					t.Errorf("expected body 'test', got %q", string(b))
				}
			},
		},
		{
			name:    "malformed request line",
			input:   "OPTIONS rtsp://example.com/media.mp4\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "invalid URL",
			input:   "OPTIONS ://example.com/media.mp4 RTSP/1.0\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "malformed headers",
			input:   "OPTIONS rtsp://example.com/media.mp4 RTSP/1.0\r\nCSeq 1\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "invalid Content-Length",
			input:   "ANNOUNCE rtsp://example.com/media.mp4 RTSP/1.0\r\nContent-Length: invalid\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "EOF",
			input:   "OPTIONS rtsp://example.com/media.mp4 RTSP/1.0\r\nCSeq: 1",
			wantErr: true,
		},
		{
			name:    "EOF before headers",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			req, err := ReadRequest(r)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && tt.check != nil {
				tt.check(t, req)
			}
		})
	}
}

func TestReadResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*testing.T, *Response)
	}{
		{
			name:    "valid without body",
			input:   "RTSP/1.0 200 OK\r\nCSeq: 1\r\n\r\n",
			wantErr: false,
			check: func(t *testing.T, resp *Response) {
				if resp.Proto != "RTSP/1.0" {
					t.Errorf("expected RTSP/1.0, got %q", resp.Proto)
				}
				if resp.StatusCode != 200 {
					t.Errorf("expected 200, got %d", resp.StatusCode)
				}
				if resp.Status != "200 OK" {
					t.Errorf("expected 200 OK, got %q", resp.Status)
				}
				if resp.Header.Get("CSeq") != "1" {
					t.Errorf("expected CSeq 1, got %q", resp.Header.Get("CSeq"))
				}
				if resp.Body != nil {
					t.Errorf("expected nil body")
				}
			},
		},
		{
			name:    "valid with body",
			input:   "RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Length: 4\r\n\r\ntest",
			wantErr: false,
			check: func(t *testing.T, resp *Response) {
				if resp.StatusCode != 200 {
					t.Errorf("expected 200, got %d", resp.StatusCode)
				}
				if resp.Body == nil {
					t.Fatalf("expected non-nil body")
				}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("failed to read body: %v", err)
				}
				if string(b) != "test" {
					t.Errorf("expected body 'test', got %q", string(b))
				}
			},
		},
		{
			name:    "malformed response line",
			input:   "RTSP/1.0\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "invalid status code",
			input:   "RTSP/1.0 OK OK\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "malformed headers",
			input:   "RTSP/1.0 200 OK\r\nCSeq 1\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "invalid Content-Length",
			input:   "RTSP/1.0 200 OK\r\nContent-Length: invalid\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "EOF",
			input:   "RTSP/1.0 200 OK\r\nCSeq: 1",
			wantErr: true,
		},
		{
			name:    "EOF before headers",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			// Passing nil as the original Request for simplicity in this test
			resp, err := ReadResponse(r, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}
