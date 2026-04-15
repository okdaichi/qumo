package amf0

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
		"number":    float64(42.5),
		"boolean":   true,
		"string":    "hello",
		"null":      nil,
		"undefined": Undefined,
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

func TestMarshalUnmarshal_ComplexTypes(t *testing.T) {
	when := time.Date(2025, 2, 3, 4, 5, 6, 789000000, time.UTC)
	in := map[string]any{
		"obj": map[string]any{
			"name": "qumo",
			"ok":   true,
		},
		"arr": StrictArray{float64(1), "two", nil},
		"ecma": ECMAArray{
			"k": "v",
		},
		"date": Date{Time: when},
	}

	data, err := Marshal(in)
	require.NoError(t, err)

	gotAny, err := Unmarshal(data)
	require.NoError(t, err)

	got, ok := gotAny.(map[string]any)
	require.True(t, ok)

	assert.Equal(t, map[string]any{"name": "qumo", "ok": true}, got["obj"])
	assert.Equal(t, StrictArray{float64(1), "two", nil}, got["arr"])
	assert.Equal(t, ECMAArray{"k": "v"}, got["ecma"])

	decodedDate, ok := got["date"].(Date)
	require.True(t, ok)
	assert.Equal(t, when.UnixMilli(), decodedDate.Time.UnixMilli())
}

func TestMarshal_LongString(t *testing.T) {
	long := bytes.Repeat([]byte{'a'}, 65536)

	data, err := Marshal(string(long))
	require.NoError(t, err)
	require.NotEmpty(t, data)
	assert.Equal(t, byte(0x0C), data[0])

	got, err := Unmarshal(data)
	require.NoError(t, err)
	assert.Equal(t, string(long), got)
}

func TestUnmarshal_InvalidMarker(t *testing.T) {
	_, err := Unmarshal([]byte{0x7F})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidMarker))
}

func TestUnmarshal_ShortInput(t *testing.T) {
	_, err := Unmarshal([]byte{0x00, 0x3f})
	require.Error(t, err)
}
