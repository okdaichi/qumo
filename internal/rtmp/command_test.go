package rtmp

import (
	"bytes"
	"net"
	"testing"

	"github.com/qumo-dev/qumo/internal/rtmp/amf0"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadCommand_AMF0(t *testing.T) {
	var buf bytes.Buffer
	enc := amf0.NewEncoder(&buf)
	require.NoError(t, enc.Encode("connect"))
	require.NoError(t, enc.Encode(float64(1)))

	obj := map[string]any{"app": "live"}
	require.NoError(t, enc.Encode(obj))

	c := newConn(nil)
	msg := &rawMessage{
		typeID:  messageTypeAMF0Command,
		payload: buf.Bytes(),
	}

	name, txID, args, err := c.readCommand(msg)
	require.NoError(t, err)
	assert.Equal(t, "connect", name)
	assert.Equal(t, float64(1), txID)
	require.Len(t, args, 1)
	assert.Equal(t, obj, args[0])
}

func TestReadCommand_AMF3(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x00) // AMF3 marker

	enc := amf0.NewEncoder(&buf)
	require.NoError(t, enc.Encode("createStream"))
	require.NoError(t, enc.Encode(float64(2)))
	require.NoError(t, enc.Encode(nil)) // null arg

	c := newConn(nil)
	msg := &rawMessage{
		typeID:  messageTypeAMF3Command,
		payload: buf.Bytes(),
	}

	name, txID, args, err := c.readCommand(msg)
	require.NoError(t, err)
	assert.Equal(t, "createStream", name)
	assert.Equal(t, float64(2), txID)
	require.Len(t, args, 1)
	assert.Nil(t, args[0])
}

func TestReadCommand_Errors(t *testing.T) {
	c := newConn(nil)

	t.Run("missing name", func(t *testing.T) {
		msg := &rawMessage{
			typeID:  messageTypeAMF0Command,
			payload: []byte{}, // empty payload
		}
		_, _, _, err := c.readCommand(msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading command name")
	})

	t.Run("missing txID", func(t *testing.T) {
		var buf bytes.Buffer
		enc := amf0.NewEncoder(&buf)
		require.NoError(t, enc.Encode("connect"))

		msg := &rawMessage{
			typeID:  messageTypeAMF0Command,
			payload: buf.Bytes(),
		}
		_, _, _, err := c.readCommand(msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading transaction ID")
	})
}

func TestReadCommand_EOF(t *testing.T) {
	c := newConn(nil)

	var buf bytes.Buffer
	enc := amf0.NewEncoder(&buf)
	require.NoError(t, enc.Encode("connect"))
	require.NoError(t, enc.Encode(float64(1)))

	msg := &rawMessage{
		typeID:  messageTypeAMF0Command,
		payload: buf.Bytes(),
	}

	name, txID, args, err := c.readCommand(msg)
	require.NoError(t, err)
	assert.Equal(t, "connect", name)
	assert.Equal(t, float64(1), txID)
	require.Empty(t, args)
}

func TestReadCommand_DecodeError(t *testing.T) {
	c := newConn(nil)

	var buf bytes.Buffer
	enc := amf0.NewEncoder(&buf)
	require.NoError(t, enc.Encode("connect"))
	require.NoError(t, enc.Encode(float64(1)))

	// Write an invalid marker to trigger a decode error
	buf.Write([]byte{0x7F})

	msg := &rawMessage{
		typeID:  messageTypeAMF0Command,
		payload: buf.Bytes(),
	}

	name, txID, args, err := c.readCommand(msg)
	require.NoError(t, err) // It swallows the error and returns what it has
	assert.Equal(t, "connect", name)
	assert.Equal(t, float64(1), txID)
	require.Empty(t, args)
}

func TestSendCommand(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writerConn := newConn(client)
	readerConn := newConn(server)

	errCh := make(chan error, 1)
	go func() {
		errCh <- writerConn.sendCommand(1, "onStatus", 0, "status", nil)
	}()

	msg, err := readerConn.readMessage()
	require.NoError(t, err)
	require.NoError(t, <-errCh)

	assert.Equal(t, messageTypeAMF0Command, msg.typeID)
	assert.Equal(t, messageStreamID(1), msg.streamID)

	name, txID, args, err := readerConn.readCommand(msg)
	require.NoError(t, err)

	assert.Equal(t, "onStatus", name)
	assert.Equal(t, float64(0), txID)
	require.Len(t, args, 2)
	assert.Equal(t, "status", args[0])
	assert.Nil(t, args[1])
}

func TestSendCommand_EncodeError(t *testing.T) {
	c := newConn(nil)

	unsupportedArg := func() {}

	err := c.sendCommand(0, "name", 1, unsupportedArg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amf0")
}

func TestReadNextCommand(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writerConn := newConn(client)
	readerConn := newConn(server)

	errCh := make(chan error, 1)
	go func() {
		// Send a non-command message first (e.g. SetChunkSize) to verify readNextCommand skips it.
		var buf bytes.Buffer
		(&messageSetChunkSize{ChunkSize: 512}).encode(&buf)
		_ = writerConn.writeRawMessage(csidControl, &rawMessage{
			typeID:  messageTypeSetChunkSize,
			payload: buf.Bytes(),
		})

		// Then send the actual command
		errCh <- writerConn.sendCommand(1, "publish", 0, nil, "stream", "live")
	}()

	name, txID, args, err := readerConn.readNextCommand()
	require.NoError(t, err)
	require.NoError(t, <-errCh)

	assert.Equal(t, "publish", name)
	assert.Equal(t, float64(0), txID)
	require.Len(t, args, 3)
	assert.Nil(t, args[0])
	assert.Equal(t, "stream", args[1])
	assert.Equal(t, "live", args[2])
}

func TestReadNextCommand_ReadMessageError(t *testing.T) {
	server, client := net.Pipe()
	client.Close() // close client immediately so server reads EOF
	server.Close()

	readerConn := newConn(server)
	_, _, _, err := readerConn.readNextCommand()
	require.Error(t, err)
}

func TestReadNextCommand_HandleControlError(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writerConn := newConn(client)
	readerConn := newConn(server)

	errCh := make(chan error, 1)
	go func() {
		// Send malformed SetChunkSize (needs 4 bytes, send 0)
		errCh <- writerConn.writeRawMessage(csidControl, &rawMessage{
			typeID:  messageTypeSetChunkSize,
			payload: []byte{},
		})
	}()

	_, _, _, err := readerConn.readNextCommand()
	require.Error(t, err)
	require.NoError(t, <-errCh)
}
