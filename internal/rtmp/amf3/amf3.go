package amf3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

const (
	markerUndefined = 0x00
	markerNull      = 0x01
	markerFalse     = 0x02
	markerTrue      = 0x03
	markerInteger   = 0x04
	markerDouble    = 0x05
	markerString    = 0x06
	markerDate      = 0x08
	markerArray     = 0x09
	markerObject    = 0x0A
	markerByteArray = 0x0C
)

const (
	maxU29         = 0x1FFFFFFF
	minSignedU29   = -268435456
	maxSignedU29   = 268435455
	maxDecodeDepth = 256
)

var (
	ErrUnsupportedType = errors.New("amf3: unsupported value type")
	ErrInvalidMarker   = errors.New("amf3: invalid marker")
	ErrInvalidRef      = errors.New("amf3: invalid reference index")
	ErrTooDeep         = errors.New("amf3: max decode depth exceeded")
)

// UndefinedType represents AMF3 undefined value.
type UndefinedType struct{}

// Undefined is the AMF3 undefined singleton value.
var Undefined = UndefinedType{}

// Array represents an AMF3 array with associative and dense sections.
type Array struct {
	Associative map[string]any
	Dense       []any
}

// Object represents AMF3 object values with class/traits metadata.
type Object struct {
	ClassName string
	Sealed    map[string]any
	Dynamic   map[string]any
}

type traitInfo struct {
	className string
	sealed    []string
	dynamic   bool
}

// Marshal encodes one AMF3 value.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes one AMF3 value.
func Unmarshal(data []byte) (any, error) {
	dec := NewDecoder(bytes.NewReader(data))
	return dec.Decode()
}

// Encoder writes AMF3 values.
type Encoder struct {
	w io.Writer

	stringRefs map[string]int
	objectRefs map[uintptr]int
}

// NewEncoder creates an AMF3 encoder.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{
		w:          w,
		stringRefs: make(map[string]int),
		objectRefs: make(map[uintptr]int),
	}
}

// Encode writes one AMF3 value.
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
		if x {
			return writeByte(e.w, markerTrue)
		}
		return writeByte(e.w, markerFalse)
	case int:
		return e.encodeInt64(int64(x))
	case int8:
		return e.encodeInt64(int64(x))
	case int16:
		return e.encodeInt64(int64(x))
	case int32:
		return e.encodeInt64(int64(x))
	case int64:
		return e.encodeInt64(x)
	case uint:
		return e.encodeUint64(uint64(x))
	case uint8:
		return e.encodeUint64(uint64(x))
	case uint16:
		return e.encodeUint64(uint64(x))
	case uint32:
		return e.encodeUint64(uint64(x))
	case uint64:
		return e.encodeUint64(x)
	case float32:
		return e.encodeDouble(float64(x))
	case float64:
		return e.encodeDouble(x)
	case string:
		if err := writeByte(e.w, markerString); err != nil {
			return err
		}
		return e.writeAMF3String(x)
	case time.Time:
		return e.encodeDate(x)
	case []any:
		return e.encodeArray(Array{Dense: x})
	case Array:
		return e.encodeArray(x)
	case map[string]any:
		return e.encodeMapObject(x)
	case Object:
		return e.encodeObject(x)
	case []byte:
		return e.encodeByteArray(x)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedType, v)
	}
}

func (e *Encoder) encodeInt64(n int64) error {
	if n >= minSignedU29 && n <= maxSignedU29 {
		if err := writeByte(e.w, markerInteger); err != nil {
			return err
		}
		return writeU29(e.w, int32(n)&maxU29)
	}
	return e.encodeDouble(float64(n))
}

func (e *Encoder) encodeUint64(n uint64) error {
	if n <= maxSignedU29 {
		if err := writeByte(e.w, markerInteger); err != nil {
			return err
		}
		return writeU29(e.w, int32(n)&maxU29)
	}
	return e.encodeDouble(float64(n))
}

func (e *Encoder) encodeDouble(f float64) error {
	if err := writeByte(e.w, markerDouble); err != nil {
		return err
	}
	return binary.Write(e.w, binary.BigEndian, f)
}

func (e *Encoder) encodeDate(t time.Time) error {
	if err := writeByte(e.w, markerDate); err != nil {
		return err
	}
	if err := writeU29(e.w, 1); err != nil {
		return err
	}
	ms := float64(t.UTC().UnixMilli())
	return binary.Write(e.w, binary.BigEndian, ms)
}

func (e *Encoder) encodeByteArray(b []byte) error {
	if err := writeByte(e.w, markerByteArray); err != nil {
		return err
	}
	if err := writeU29(e.w, int32((len(b)<<1)|1)); err != nil {
		return err
	}
	_, err := e.w.Write(b)
	return err
}

func (e *Encoder) encodeArray(arr Array) error {
	if err := writeByte(e.w, markerArray); err != nil {
		return err
	}
	if err := writeU29(e.w, int32((len(arr.Dense)<<1)|1)); err != nil {
		return err
	}

	keys := sortedKeys(arr.Associative)
	for _, k := range keys {
		if err := e.writeAMF3String(k); err != nil {
			return err
		}
		if err := e.encodeValue(arr.Associative[k]); err != nil {
			return err
		}
	}
	if err := e.writeAMF3String(""); err != nil {
		return err
	}

	for _, v := range arr.Dense {
		if err := e.encodeValue(v); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) encodeMapObject(m map[string]any) error {
	obj := Object{ClassName: "", Sealed: nil, Dynamic: m}
	return e.encodeObject(obj)
}

func (e *Encoder) encodeObject(obj Object) error {
	if err := writeByte(e.w, markerObject); err != nil {
		return err
	}

	var traitHeader int32
	if len(obj.Sealed) > 0 {
		traitHeader = int32((len(obj.Sealed) << 4) | 0x03)
	} else {
		traitHeader = 0x0B // dynamic, inline object, inline traits, no sealed members
	}
	if err := writeU29(e.w, traitHeader); err != nil {
		return err
	}

	if err := e.writeAMF3String(obj.ClassName); err != nil {
		return err
	}

	if len(obj.Sealed) > 0 {
		sealedKeys := sortedKeys(obj.Sealed)
		for _, k := range sealedKeys {
			if err := e.writeAMF3String(k); err != nil {
				return err
			}
		}
		for _, k := range sealedKeys {
			if err := e.encodeValue(obj.Sealed[k]); err != nil {
				return err
			}
		}
		return nil
	}

	dynKeys := sortedKeys(obj.Dynamic)
	for _, k := range dynKeys {
		if err := e.writeAMF3String(k); err != nil {
			return err
		}
		if err := e.encodeValue(obj.Dynamic[k]); err != nil {
			return err
		}
	}
	return e.writeAMF3String("")
}

func (e *Encoder) writeAMF3String(s string) error {
	if s == "" {
		return writeU29(e.w, 1)
	}
	if idx, ok := e.stringRefs[s]; ok {
		return writeU29(e.w, int32(idx<<1))
	}
	b := []byte(s)
	if err := writeU29(e.w, int32((len(b)<<1)|1)); err != nil {
		return err
	}
	if _, err := e.w.Write(b); err != nil {
		return err
	}
	e.stringRefs[s] = len(e.stringRefs)
	return nil
}

// Decoder reads AMF3 values.
type Decoder struct {
	r io.Reader

	stringRefs []string
	objectRefs []any
	traitRefs  []traitInfo
}

// NewDecoder creates an AMF3 decoder.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// Decode reads one AMF3 value.
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
	case markerUndefined:
		return Undefined, nil
	case markerNull:
		return nil, nil
	case markerFalse:
		return false, nil
	case markerTrue:
		return true, nil
	case markerInteger:
		u, err := readU29(d.r)
		if err != nil {
			return nil, err
		}
		return decodeSignedU29(u), nil
	case markerDouble:
		var f float64
		if err := binary.Read(d.r, binary.BigEndian, &f); err != nil {
			return nil, err
		}
		return f, nil
	case markerString:
		return d.readAMF3String()
	case markerDate:
		return d.decodeDate()
	case markerArray:
		return d.decodeArray(depth + 1)
	case markerObject:
		return d.decodeObject(depth + 1)
	case markerByteArray:
		return d.decodeByteArray()
	default:
		return nil, fmt.Errorf("%w: 0x%02x", ErrInvalidMarker, m)
	}
}

func (d *Decoder) decodeDate() (any, error) {
	u, err := readU29(d.r)
	if err != nil {
		return nil, err
	}
	if u&1 == 0 {
		idx := int(u >> 1)
		if idx < 0 || idx >= len(d.objectRefs) {
			return nil, ErrInvalidRef
		}
		return d.objectRefs[idx], nil
	}

	var ms float64
	if err := binary.Read(d.r, binary.BigEndian, &ms); err != nil {
		return nil, err
	}
	t := time.UnixMilli(int64(ms)).UTC()
	d.objectRefs = append(d.objectRefs, t)
	return t, nil
}

func (d *Decoder) decodeByteArray() (any, error) {
	u, err := readU29(d.r)
	if err != nil {
		return nil, err
	}
	if u&1 == 0 {
		idx := int(u >> 1)
		if idx < 0 || idx >= len(d.objectRefs) {
			return nil, ErrInvalidRef
		}
		return d.objectRefs[idx], nil
	}
	ln := int(u >> 1)
	b := make([]byte, ln)
	if _, err := io.ReadFull(d.r, b); err != nil {
		return nil, err
	}
	d.objectRefs = append(d.objectRefs, b)
	return b, nil
}

func (d *Decoder) decodeArray(depth int) (any, error) {
	u, err := readU29(d.r)
	if err != nil {
		return nil, err
	}
	if u&1 == 0 {
		idx := int(u >> 1)
		if idx < 0 || idx >= len(d.objectRefs) {
			return nil, ErrInvalidRef
		}
		return d.objectRefs[idx], nil
	}

	denseLen := int(u >> 1)
	assoc := make(map[string]any)

	for {
		key, err := d.readAMF3String()
		if err != nil {
			return nil, err
		}
		s, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("amf3: associative key must be string, got %T", key)
		}
		if s == "" {
			break
		}
		v, err := d.decodeValue(depth + 1)
		if err != nil {
			return nil, err
		}
		assoc[s] = v
	}

	dense := make([]any, denseLen)
	if len(assoc) == 0 {
		d.objectRefs = append(d.objectRefs, dense)
	} else {
		d.objectRefs = append(d.objectRefs, Array{Associative: assoc, Dense: dense})
	}
	idx := len(d.objectRefs) - 1

	for i := range denseLen {
		v, err := d.decodeValue(depth + 1)
		if err != nil {
			return nil, err
		}
		dense[i] = v
	}

	if len(assoc) == 0 {
		d.objectRefs[idx] = dense
		return dense, nil
	}
	out := Array{Associative: assoc, Dense: dense}
	d.objectRefs[idx] = out
	return out, nil
}

func (d *Decoder) decodeObject(depth int) (any, error) {
	u, err := readU29(d.r)
	if err != nil {
		return nil, err
	}

	if u&1 == 0 {
		idx := int(u >> 1)
		if idx < 0 || idx >= len(d.objectRefs) {
			return nil, ErrInvalidRef
		}
		return d.objectRefs[idx], nil
	}

	var traits traitInfo
	if u&0x02 == 0 {
		traitIdx := int(u >> 2)
		if traitIdx < 0 || traitIdx >= len(d.traitRefs) {
			return nil, ErrInvalidRef
		}
		traits = d.traitRefs[traitIdx]
	} else {
		externalizable := (u & 0x04) != 0
		dynamic := (u & 0x08) != 0
		sealedCount := int(u >> 4)

		classNameAny, err := d.readAMF3String()
		if err != nil {
			return nil, err
		}
		className, ok := classNameAny.(string)
		if !ok {
			return nil, fmt.Errorf("amf3: class name must be string, got %T", classNameAny)
		}

		sealed := make([]string, 0, sealedCount)
		for range sealedCount {
			nameAny, err := d.readAMF3String()
			if err != nil {
				return nil, err
			}
			name, ok := nameAny.(string)
			if !ok {
				return nil, fmt.Errorf("amf3: sealed key must be string, got %T", nameAny)
			}
			sealed = append(sealed, name)
		}

		if externalizable {
			return nil, errors.New("amf3: externalizable object is not supported")
		}

		traits = traitInfo{className: className, sealed: sealed, dynamic: dynamic}
		d.traitRefs = append(d.traitRefs, traits)
	}

	sealedValues := make(map[string]any, len(traits.sealed))
	out := Object{ClassName: traits.className, Sealed: sealedValues, Dynamic: nil}
	d.objectRefs = append(d.objectRefs, out)
	objIdx := len(d.objectRefs) - 1

	for _, k := range traits.sealed {
		v, err := d.decodeValue(depth + 1)
		if err != nil {
			return nil, err
		}
		sealedValues[k] = v
	}

	if traits.dynamic {
		out.Dynamic = make(map[string]any)
		for {
			nameAny, err := d.readAMF3String()
			if err != nil {
				return nil, err
			}
			name, ok := nameAny.(string)
			if !ok {
				return nil, fmt.Errorf("amf3: dynamic key must be string, got %T", nameAny)
			}
			if name == "" {
				break
			}
			v, err := d.decodeValue(depth + 1)
			if err != nil {
				return nil, err
			}
			out.Dynamic[name] = v
		}
	}

	if out.ClassName == "" && len(out.Sealed) == 0 {
		if out.Dynamic == nil {
			out.Dynamic = map[string]any{}
		}
		d.objectRefs[objIdx] = out.Dynamic
		return out.Dynamic, nil
	}

	d.objectRefs[objIdx] = out
	return out, nil
}

func (d *Decoder) readAMF3String() (any, error) {
	u, err := readU29(d.r)
	if err != nil {
		return nil, err
	}
	if u&1 == 0 {
		idx := int(u >> 1)
		if idx < 0 || idx >= len(d.stringRefs) {
			return nil, ErrInvalidRef
		}
		return d.stringRefs[idx], nil
	}

	ln := int(u >> 1)
	if ln == 0 {
		return "", nil
	}
	b := make([]byte, ln)
	if _, err := io.ReadFull(d.r, b); err != nil {
		return nil, err
	}
	s := string(b)
	d.stringRefs = append(d.stringRefs, s)
	return s, nil
}

func decodeSignedU29(u int32) int32 {
	if u&0x10000000 != 0 {
		return u - 0x20000000
	}
	return u
}

func writeU29(w io.Writer, v int32) error {
	if v < 0 || v > maxU29 {
		return fmt.Errorf("amf3: u29 out of range: %d", v)
	}

	var buf [4]byte
	var n int
	switch {
	case v < 0x80:
		buf[0] = byte(v)
		n = 1
	case v < 0x4000:
		buf[0] = byte((v>>7)&0x7F | 0x80)
		buf[1] = byte(v & 0x7F)
		n = 2
	case v < 0x200000:
		buf[0] = byte((v>>14)&0x7F | 0x80)
		buf[1] = byte((v>>7)&0x7F | 0x80)
		buf[2] = byte(v & 0x7F)
		n = 3
	default:
		buf[0] = byte((v>>22)&0x7F | 0x80)
		buf[1] = byte((v>>15)&0x7F | 0x80)
		buf[2] = byte((v>>8)&0x7F | 0x80)
		buf[3] = byte(v)
		n = 4
	}
	_, err := w.Write(buf[:n])
	return err
}

func readU29(r io.Reader) (int32, error) {
	var b [1]byte
	var v int32

	for range 3 {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		v = (v << 7) | int32(b[0]&0x7F)
		if b[0]&0x80 == 0 {
			return v, nil
		}
	}

	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	v = (v << 8) | int32(b[0])
	return v, nil
}

func writeByte(w io.Writer, b byte) error {
	_, err := w.Write([]byte{b})
	return err
}

func readByte(r io.Reader) (byte, error) {
	buf := make([]byte, 1)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, len(m))
	i := 0
	for k := range m {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	return keys
}
