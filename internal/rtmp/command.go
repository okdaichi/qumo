package rtmp

import (
	"fmt"
	"io"

	"github.com/okdaichi/qumo/internal/rtmp/amf0"
	"github.com/okdaichi/qumo/internal/rtmp/amf3"
)

// AMFValue represents a single AMF payload value.
// It is kept as an alias to preserve flexibility while giving semantic intent.
type AMFValue = any

// AMFObject represents an AMF object value.
type AMFObject map[string]AMFValue

type CommandMessageFormat string

const (
	CommandMessageFormatAMF0 CommandMessageFormat = "AMF0"
	CommandMessageFormatAMF3 CommandMessageFormat = "AMF3"
)

type CommandStream struct {
	messageFormat CommandMessageFormat
}

type CommandMessageConnect struct {
	Format            CommandMessageFormat
	TransactionID     float64
	CommandObject     AMFObject
	OptionalArguments []AMFValue
}

func (c *CommandMessageConnect) encode(w io.Writer) error {
	var encoder interface{ Encode(any) error }
	if c.Format == CommandMessageFormatAMF3 {
		encoder = amf3.NewEncoder(w)
	} else {
		encoder = amf0.NewEncoder(w)
	}

	if err := encoder.Encode(commandMessageNameConnect); err != nil {
		return err
	}
	if err := encoder.Encode(c.TransactionID); err != nil {
		return err
	}
	if err := encoder.Encode(map[string]any(c.CommandObject)); err != nil {
		return err
	}
	for _, arg := range c.OptionalArguments {
		if err := encoder.Encode(arg); err != nil {
			return err
		}
	}
	return nil
}

func (c *CommandMessageConnect) decode(r io.Reader) error {
	var decoder interface{ Decode() (any, error) }
	if c.Format == CommandMessageFormatAMF3 {
		decoder = amf3.NewDecoder(r)
	} else {
		decoder = amf0.NewDecoder(r)
	}

	// Decode transaction ID
	tid, err := decoder.Decode()
	if err != nil {
		return err
	}
	if tidNum, ok := tid.(float64); ok {
		c.TransactionID = tidNum
	} else {
		return fmt.Errorf("invalid transaction ID type: %T", tid)
	}

	// Decode command object
	cmdObj, err := decoder.Decode()
	if err != nil {
		return err
	}
	if obj, ok := cmdObj.(map[string]any); ok {
		c.CommandObject = AMFObject(obj)
	} else {
		return fmt.Errorf("invalid command object type: %T", cmdObj)
	}

	// Decode optional arguments
	for {
		arg, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		c.OptionalArguments = append(c.OptionalArguments, arg)
	}

	return nil
}

const (
	commandMessageNameConnect      = "connect"
	commandMessageNameCall         = "call"
	commandMessageNameClose        = "close"
	commandMessageNameCreateStream = "createStream"
)

const (
	commandMessageNamePlay    = "play"
	commandMessageNamePublish = "publish"

	// Professional/Common encoder publish commands
	commandMessageNameReleaseStream = "releaseStream"
	commandMessageNameFCPublish     = "FCPublish"
	commandMessageNameFCUnpublish   = "FCUnpublish"
	commandMessageNameDeleteStream  = "deleteStream"

	// Server responses
	commandMessageNameResult   = "_result"
	commandMessageNameError    = "_error"
	commandMessageNameOnStatus = "onStatus"
)

type CommandMessageCreateStream struct {
	Format        CommandMessageFormat
	TransactionID float64
	CommandObject AMFObject
}

func (c *CommandMessageCreateStream) encode(w io.Writer) error {
	var encoder interface{ Encode(any) error }
	if c.Format == CommandMessageFormatAMF3 {
		encoder = amf3.NewEncoder(w)
	} else {
		encoder = amf0.NewEncoder(w)
	}

	if err := encoder.Encode(commandMessageNameCreateStream); err != nil {
		return err
	}
	if err := encoder.Encode(c.TransactionID); err != nil {
		return err
	}
	// CreateStream typically sends null for command object
	if c.CommandObject == nil {
		// encoding nil sends AMF Null
		if err := encoder.Encode(nil); err != nil {
			return err
		}
	} else {
		if err := encoder.Encode(map[string]any(c.CommandObject)); err != nil {
			return err
		}
	}

	return nil
}

func (c *CommandMessageCreateStream) decode(r io.Reader) error {
	var decoder interface{ Decode() (any, error) }
	if c.Format == CommandMessageFormatAMF3 {
		decoder = amf3.NewDecoder(r)
	} else {
		decoder = amf0.NewDecoder(r)
	}

	// Decode transaction ID
	tid, err := decoder.Decode()
	if err != nil {
		return err
	}
	if tidNum, ok := tid.(float64); ok {
		c.TransactionID = tidNum
	} else {
		return fmt.Errorf("invalid transaction ID type: %T", tid)
	}

	// Decode command object
	cmdObj, err := decoder.Decode()
	if err != nil {
		return err
	}
	if cmdObj == nil {
		c.CommandObject = nil
	} else if obj, ok := cmdObj.(map[string]any); ok {
		c.CommandObject = AMFObject(obj)
	} else {
		// Not strictly mapping an error, sometimes it might be just null
		return fmt.Errorf("invalid command object type: %T", cmdObj)
	}

	return nil
}

// CommandMessagePublish represents a "publish" command.
type CommandMessagePublish struct {
	Format         CommandMessageFormat
	TransactionID  float64
	CommandObject  AMFObject // Usually null
	PublishingName string
	PublishingType string // "live", "record", or "append"
}

func (c *CommandMessagePublish) encode(w io.Writer) error {
	var encoder interface{ Encode(any) error }
	if c.Format == CommandMessageFormatAMF3 {
		encoder = amf3.NewEncoder(w)
	} else {
		encoder = amf0.NewEncoder(w)
	}

	if err := encoder.Encode(commandMessageNamePublish); err != nil {
		return err
	}
	if err := encoder.Encode(c.TransactionID); err != nil {
		return err
	}
	if c.CommandObject == nil {
		if err := encoder.Encode(nil); err != nil {
			return err
		}
	} else {
		if err := encoder.Encode(map[string]any(c.CommandObject)); err != nil {
			return err
		}
	}
	if err := encoder.Encode(c.PublishingName); err != nil {
		return err
	}
	if err := encoder.Encode(c.PublishingType); err != nil {
		return err
	}
	return nil
}

func (c *CommandMessagePublish) decode(r io.Reader) error {
	var decoder interface{ Decode() (any, error) }
	if c.Format == CommandMessageFormatAMF3 {
		decoder = amf3.NewDecoder(r)
	} else {
		decoder = amf0.NewDecoder(r)
	}

	tid, err := decoder.Decode()
	if err != nil {
		return err
	}
	if tidNum, ok := tid.(float64); ok {
		c.TransactionID = tidNum
	}

	cmdObj, err := decoder.Decode()
	if err != nil {
		return err
	}
	if cmdObj == nil {
		c.CommandObject = nil
	} else if obj, ok := cmdObj.(map[string]any); ok {
		c.CommandObject = AMFObject(obj)
	}

	pubName, err := decoder.Decode()
	if err != nil {
		return err
	}
	if nameStr, ok := pubName.(string); ok {
		c.PublishingName = nameStr
	}

	pubType, err := decoder.Decode()
	if err != nil {
		return err
	}
	if typeStr, ok := pubType.(string); ok {
		c.PublishingType = typeStr
	}

	return nil
}

// CommandMessage represents a generic command used for parsing unknown or ignored commands.
type CommandMessage struct {
	Format        CommandMessageFormat
	Name          string
	TransactionID float64
	Args          []any
}

func (c *CommandMessage) encode(w io.Writer) error {
	var encoder interface{ Encode(any) error }
	if c.Format == CommandMessageFormatAMF3 {
		encoder = amf3.NewEncoder(w)
	} else {
		encoder = amf0.NewEncoder(w)
	}

	if err := encoder.Encode(c.Name); err != nil {
		return err
	}
	if err := encoder.Encode(c.TransactionID); err != nil {
		return err
	}
	for _, arg := range c.Args {
		if err := encoder.Encode(arg); err != nil {
			return err
		}
	}
	return nil
}

func (c *CommandMessage) decode(r io.Reader) error {
	var decoder interface{ Decode() (any, error) }
	if c.Format == CommandMessageFormatAMF3 {
		decoder = amf3.NewDecoder(r)
	} else {
		decoder = amf0.NewDecoder(r)
	}

	// We assume that the Name was already read to determine this is a generic command,
	// but if this decode method is responsible for reading *after* Name:
	// Let's read TransactionID first.
	tid, err := decoder.Decode()
	if err != nil {
		return err
	}
	if tidNum, ok := tid.(float64); ok {
		c.TransactionID = tidNum
	}

	// Read remaining arguments until EOF
	for {
		arg, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		c.Args = append(c.Args, arg)
	}

	return nil
}

const (
	// Legacy/RTMP core audio codec capabilities.
	// Raw sound, no compression.
	audioCodecFlagNone = 0x0001
	// MP3.
	audioCodecFlagMP3 = 0x0004
	// G.711 A-law logarithmic PCM.
	audioCodecFlagG711A = 0x0080
	// G.711 mu-law logarithmic PCM.
	audioCodecFlagG711Mu = 0x0100
	// AAC.
	audioCodecFlagAAC = 0x0400
	// Speex.
	audioCodecFlagSpeex = 0x0800

	// Enhanced RTMP audio codec capabilities.
	// Opus (Enhanced RTMP / extended audio).
	audioCodecFlagOpus = 0x2000
	// FLAC (Enhanced RTMP / extended audio).
	audioCodecFlagFLAC = 0x4000
)

const (
	// H.264/AVC (required by common RTMP publishers such as OBS).
	videoCodecFlagH264 = 0x0080

	// Enhanced RTMP video codec capabilities.
	// HEVC/H.265 (Enhanced RTMP / extended video).
	videoCodecFlagHEVC = 0x0100
	// AV1 (Enhanced RTMP / extended video).
	videoCodecFlagAV1 = 0x0200
	// VP9 (Enhanced RTMP / extended video).
	videoCodecFlagVP9 = 0x0400
)

const (
	objectEncodingAMF0 = 0
	objectEncodingAMF3 = 3
)
