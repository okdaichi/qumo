package amf3

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalUnmarshal_Primitives(t *testing.T) {
	tests := map[string]any{
		"undefined": Undefined,
		"null":      nil,
		"false":     false,
		"true":      true,
		"integer":   int32(42),
		"double":    3.14159,
		"string":    "hello",
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := Marshal(in)
			require.NoError(t, err)

			got, err := Unmarshal(data)
			require.NoError(t, err)

			assert.Equal(t, in, got)
		})
	}
}

func TestMarshal_IntegerBoundary(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want any
	}{
		{name: "min signed u29", in: -268435456, want: int32(-268435456)},
		{name: "max signed u29", in: 268435455, want: int32(268435455)},
		{name: "outside u29 goes double", in: 268435456, want: float64(268435456)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := Marshal(tt.in)
			require.NoError(t, err)
			got, err := Unmarshal(data)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStringReferenceDecode(t *testing.T) {
	// markerString + inline("abc") + markerString + ref(index0)
	data := []byte{0x06, 0x07, 'a', 'b', 'c', 0x06, 0x00}

	dec := NewDecoder(bytes.NewReader(data))
	v1, err := dec.Decode()
	require.NoError(t, err)
	v2, err := dec.Decode()
	require.NoError(t, err)

	assert.Equal(t, "abc", v1)
	assert.Equal(t, "abc", v2)
}

func TestMarshalUnmarshal_Array(t *testing.T) {
	in := Array{
		Associative: map[string]any{"kind": "meta"},
		Dense:       []any{int32(1), "two", true},
	}

	data, err := Marshal(in)
	require.NoError(t, err)

	gotAny, err := Unmarshal(data)
	require.NoError(t, err)

	got, ok := gotAny.(Array)
	require.True(t, ok)
	assert.Equal(t, in.Associative, got.Associative)
	assert.Equal(t, []any{int32(1), "two", true}, got.Dense)
}

func TestMarshalUnmarshal_DynamicObjectAsMap(t *testing.T) {
	in := map[string]any{"name": "qumo", "ok": true, "n": int32(7)}

	data, err := Marshal(in)
	require.NoError(t, err)

	gotAny, err := Unmarshal(data)
	require.NoError(t, err)

	got, ok := gotAny.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, in, got)
}

func TestMarshalUnmarshal_Date(t *testing.T) {
	in := time.Date(2025, 8, 7, 6, 5, 4, 321000000, time.UTC)

	data, err := Marshal(in)
	require.NoError(t, err)

	gotAny, err := Unmarshal(data)
	require.NoError(t, err)

	got, ok := gotAny.(time.Time)
	require.True(t, ok)
	assert.Equal(t, in.UnixMilli(), got.UnixMilli())
}

func TestMarshalUnmarshal_ByteArray(t *testing.T) {
	in := []byte{0x01, 0x02, 0x03}

	data, err := Marshal(in)
	require.NoError(t, err)

	gotAny, err := Unmarshal(data)
	require.NoError(t, err)

	got, ok := gotAny.([]byte)
	require.True(t, ok)
	assert.Equal(t, in, got)
}

func TestUnmarshal_InvalidMarker(t *testing.T) {
	_, err := Unmarshal([]byte{0x7F})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidMarker))
}

func TestUnmarshal_InvalidStringRef(t *testing.T) {
	// markerString + ref index 1 (but no refs exist)
	_, err := Unmarshal([]byte{0x06, 0x02})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidRef))
}
