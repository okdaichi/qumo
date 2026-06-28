package rtsp

import (
	"reflect"
	"testing"
)

func TestParseSDP(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected *SDP
	}{
		{
			name: "basic video and audio",
			data: "v=0\r\n" +
				"o=- 0 0 IN IP4 127.0.0.1\r\n" +
				"s=No Name\r\n" +
				"c=IN IP4 127.0.0.1\r\n" +
				"t=0 0\r\n" +
				"a=tool:libavformat 58.76.100\r\n" +
				"m=video 0 RTP/AVP 96\r\n" +
				"a=rtpmap:96 H264/90000\r\n" +
				"a=fmtp:96 packetization-mode=1; sprop-parameter-sets=Z0LAKp2oHgCJ+WbgICAoAAADAAgAAAMBlCA=,aM48gA==; profile-level-id=42C02A\r\n" +
				"a=control:streamid=0\r\n" +
				"m=audio 0 RTP/AVP 97\r\n" +
				"b=AS:128\r\n" +
				"a=rtpmap:97 MPEG4-GENERIC/44100/2\r\n" +
				"a=fmtp:97 profile-level-id=1;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3; config=1210\r\n" +
				"a=control:streamid=1\r\n",
			expected: &SDP{
				Medias: []SDPMedia{
					{
						Type:    "video",
						Control: "streamid=0",
						RtpMap:  "96 H264/90000",
						Fmtp:    "96 packetization-mode=1; sprop-parameter-sets=Z0LAKp2oHgCJ+WbgICAoAAADAAgAAAMBlCA=,aM48gA==; profile-level-id=42C02A",
						Attributes: map[string]string{
							"rtpmap":  "96 H264/90000",
							"fmtp":    "96 packetization-mode=1; sprop-parameter-sets=Z0LAKp2oHgCJ+WbgICAoAAADAAgAAAMBlCA=,aM48gA==; profile-level-id=42C02A",
							"control": "streamid=0",
						},
					},
					{
						Type:    "audio",
						Control: "streamid=1",
						RtpMap:  "97 MPEG4-GENERIC/44100/2",
						Fmtp:    "97 profile-level-id=1;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3; config=1210",
						Attributes: map[string]string{
							"rtpmap":  "97 MPEG4-GENERIC/44100/2",
							"fmtp":    "97 profile-level-id=1;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3; config=1210",
							"control": "streamid=1",
						},
					},
				},
			},
		},
		{
			name: "attributes without colon",
			data: "m=video 0 RTP/AVP 96\n" +
				"a=recvonly\n" +
				"a=sendrecv\n",
			expected: &SDP{
				Medias: []SDPMedia{
					{
						Type: "video",
						Attributes: map[string]string{
							"recvonly": "",
							"sendrecv": "",
						},
					},
				},
			},
		},
		{
			name: "ignore attributes before media",
			data: "v=0\n" +
				"a=global:attr\n" +
				"m=audio 0 RTP/AVP 97\n" +
				"a=local:attr\n",
			expected: &SDP{
				Medias: []SDPMedia{
					{
						Type: "audio",
						Attributes: map[string]string{
							"local": "attr",
						},
					},
				},
			},
		},
		{
			name: "malformed lines and empty lines",
			data: "\n" +
				"m=video 0 RTP/AVP 96\n" +
				"\n" +
				"malformed_line_no_equals\n" +
				"m\n" +
				"a=control:streamid=0\n",
			expected: &SDP{
				Medias: []SDPMedia{
					{
						Type:    "video",
						Control: "streamid=0",
						Attributes: map[string]string{
							"control": "streamid=0",
						},
					},
				},
			},
		},
		{
			name:     "empty input",
			data:     "",
			expected: &SDP{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ParseSDP(tt.data)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("ParseSDP() = %+v, expected %+v", actual, tt.expected)
			}
		})
	}
}
