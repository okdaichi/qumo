package amf0

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

const (
	markerNumber      = 0x00
	markerBoolean     = 0x01
	markerString      = 0x02
	markerObject      = 0x03
	markerNull        = 0x05
	markerUndefined   = 0x06
	markerECMAArray   = 0x08
	markerObjectEnd   = 0x09
	markerStrictArray = 0x0A
	markerDate        = 0x0B
	markerLongString  = 0x0C
)

const maxDecodeDepth = 256

var (
	ErrUnsupportedType = errors.New("amf0: unsupported value type")
	ErrInvalidMarker   = errors.New("amf0: invalid marker")
	ErrTooDeep         = errors.New("amf0: max decode depth exceeded")
)

// UndefinedType represents AMF0 undefined value.
type UndefinedType struct{}

// Undefined is the AMF0 undefined singleton value.
var Undefined = UndefinedType{}

// ECMAArray represents AMF0 ECMA array (associative array).
type ECMAArray map[string]any

// StrictArray represents AMF0 strict (indexed) array.
type StrictArray []any

// Date represents AMF0 date value.
type Date struct {
	Time time.Time
}

// Marshal encodes one AMF0 value.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes one AMF0 value from the given bytes.
func Unmarshal(data []byte) (any, error) {
	dec := NewDecoder(bytes.NewReader(data))
	return dec.Decode()
}

// Encoder writes AMF0 values.
type Encoder struct {
	w io.Writer
}

// NewEncoder creates an AMF0 encoder.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Encode writes one AMF0 value.
func (e *Encoder) Encode(v any) error {
	return e.encodeValue(v)
}

func (e *Encoder) encodeValue(v any) error {
	switch x := v.(type) {
	case nil:
		return writeByte(e.w, markerNull)
	case UndefinedType:
		return writeByte(e.w, markerUndefined)
	case bool:
		if err := writeByte(e.w, markerBoolean); err != nil {
			return err
		}
		if x {
			return writeByte(e.w, 1)
		}
		return writeByte(e.w, 0)
	case float64:
		if err := writeByte(e.w, markerNumber); err != nil {
			return err
		}
		return binary.Write(e.w, binary.BigEndian, x)
	case float32:
		if err := writeByte(e.w, markerNumber); err != nil {
			return err
		}
		return binary.Write(e.w, binary.BigEndian, float64(x))
	case int:
		return e.encodeValue(float64(x))
	case int8:
		return e.encodeValue(float64(x))
	case int16:
		return e.encodeValue(float64(x))
	case int32:
		return e.encodeValue(float64(x))
	case int64:
		return e.encodeValue(float64(x))
	case uint:
		return e.encodeValue(float64(x))
	case uint8:
		return e.encodeValue(float64(x))
	case uint16:
		return e.encodeValue(float64(x))
	case uint32:
		return e.encodeValue(float64(x))
	case uint64:
		return e.encodeValue(float64(x))
	case string:
		return encodeString(e.w, x)
	case map[string]any:
		if err := writeByte(e.w, markerObject); err != nil {
			return err
		}
		keys := sortedKeys(x)
		for _, k := range keys {
			if err := writeUTF8(e.w, k); err != nil {
				return err
			}
			if err := e.encodeValue(x[k]); err != nil {
				return err
			}
		}
		return writeObjectEnd(e.w)
	case ECMAArray:
		if err := writeByte(e.w, markerECMAArray); err != nil {
			return err
		}
		if err := writeU32(e.w, uint32(len(x))); err != nil {
			return err
		}
		keys := sortedKeys(x)
		for _, k := range keys {
			if err := writeUTF8(e.w, k); err != nil {
				return err
			}
			if err := e.encodeValue(x[k]); err != nil {
				return err
			}
		}
		return writeObjectEnd(e.w)
	case StrictArray:
		if err := writeByte(e.w, markerStrictArray); err != nil {
			return err
		}
		if err := writeU32(e.w, uint32(len(x))); err != nil {
			return err
		}
		for _, item := range x {
			if err := e.encodeValue(item); err != nil {
				return err
			}
		}
		return nil
	case Date:
		if err := writeByte(e.w, markerDate); err != nil {
			return err
		}
		ms := float64(x.Time.UTC().UnixMilli())
		if err := binary.Write(e.w, binary.BigEndian, ms); err != nil {
			return err
		}
		return writeU16(e.w, 0)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedType, v)
	}
}

// Decoder reads AMF0 values.
type Decoder struct {
	r io.Reader
}

// NewDecoder creates an AMF0 decoder.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// Decode reads one AMF0 value.
func (d *Decoder) Decode() (any, error) {
	return d.decodeValue(0)
}

func (d *Decoder) decodeValue(depth int) (any, error) {
	if depth > maxDecodeDepth {
		return nil, ErrTooDeep
	}

	m, err := readByte(d.r)
	if err != nil {
		return nil, err
	}

	switch m {
	case markerNumber:
		var n float64
		if err := binary.Read(d.r, binary.BigEndian, &n); err != nil {
			return nil, err
		}
		return n, nil
	case markerBoolean:
		b, err := readByte(d.r)
		if err != nil {
			return nil, err
		}
		return b != 0, nil
	case markerString:
		s, err := readUTF8(d.r)
		if err != nil {
			return nil, err
		}
		return s, nil
	case markerLongString:
		s, err := readLongUTF8(d.r)
		if err != nil {
			return nil, err
		}
		return s, nil
	case markerObject:
		return d.decodeObject(depth + 1)
	case markerNull:
		return nil, nil
	case markerUndefined:
		return Undefined, nil
	case markerECMAArray:
		_, err := readU32(d.r)
		if err != nil {
			return nil, err
		}
		obj, err := d.decodeObject(depth + 1)
		if err != nil {
			return nil, err
		}
		m, ok := obj.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("amf0: invalid ECMA array payload type %T", obj)
		}
		return ECMAArray(m), nil
	case markerStrictArray:
		ln, err := readU32(d.r)
		if err != nil {
			return nil, err
		}
		arr := make(StrictArray, 0, ln)
		for range ln {
			v, err := d.decodeValue(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case markerDate:
		var ms float64
		if err := binary.Read(d.r, binary.BigEndian, &ms); err != nil {
			return nil, err
		}
		if _, err := readU16(d.r); err != nil {
			return nil, err
		}
		if math.IsNaN(ms) || math.IsInf(ms, 0) {
			return Date{Time: time.UnixMilli(0).UTC()}, nil
		}
		return Date{Time: time.UnixMilli(int64(ms)).UTC()}, nil
	default:
		return nil, fmt.Errorf("%w: 0x%02x", ErrInvalidMarker, m)
	}
}

func (d *Decoder) decodeObject(depth int) (any, error) {
	obj := make(map[string]any)
	for {
		key, err := readUTF8(d.r)
		if err != nil {
			return nil, err
		}
		if key == "" {
			m, err := readByte(d.r)
			if err != nil {
				return nil, err
			}
			if m != markerObjectEnd {
				return nil, fmt.Errorf("amf0: object terminator expected, got 0x%02x", m)
			}
			return obj, nil
		}
		v, err := d.decodeValue(depth + 1)
		if err != nil {
			return nil, err
		}
		obj[key] = v
	}
}

func encodeString(w io.Writer, s string) error {
	b := []byte(s)
	if len(b) <= math.MaxUint16 {
		if err := writeByte(w, markerString); err != nil {
			return err
		}
		if err := writeU16(w, uint16(len(b))); err != nil {
			return err
		}
		_, err := w.Write(b)
		return err
	}

	if err := writeByte(w, markerLongString); err != nil {
		return err
	}
	if err := writeU32(w, uint32(len(b))); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func writeObjectEnd(w io.Writer) error {
	if err := writeU16(w, 0); err != nil {
		return err
	}
	return writeByte(w, markerObjectEnd)
}

func writeByte(w io.Writer, b byte) error {
	var buf [1]byte
	buf[0] = b
	_, err := w.Write(buf[:])
	return err
}

func readByte(r io.Reader) (byte, error) {
	var buf [1]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

func writeU16(w io.Writer, v uint16) error {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func readU16(r io.Reader) (uint16, error) {
	var buf [2]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(buf[:]), nil
}

func writeU32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func readU32(r io.Reader) (uint32, error) {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

func readUTF8(r io.Reader) (string, error) {
	ln, err := readU16(r)
	if err != nil {
		return "", err
	}
	if ln == 0 {
		return "", nil
	}
	b := make([]byte, ln)
	_, err = io.ReadFull(r, b)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func readLongUTF8(r io.Reader) (string, error) {
	ln, err := readU32(r)
	if err != nil {
		return "", err
	}
	if ln == 0 {
		return "", nil
	}
	b := make([]byte, ln)
	_, err = io.ReadFull(r, b)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeUTF8(w io.Writer, s string) error {
	b := []byte(s)
	if len(b) > math.MaxUint16 {
		return fmt.Errorf("amf0: object key too long: %d", len(b))
	}
	if err := writeU16(w, uint16(len(b))); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
