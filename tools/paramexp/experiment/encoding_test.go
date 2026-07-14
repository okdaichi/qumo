package experiment

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecode_Continuous(t *testing.T) {
	space := ParamSpace{Params: []ParamDef{
		{Name: "x", Type: TypeContinuous, Min: 0, Max: 10},
	}}
	enc, err := NewEncoder(space)
	require.NoError(t, err)

	u, err := enc.Encode(ParamVector{"x": "5"})
	require.NoError(t, err)
	assert.InDelta(t, 0.5, u[0], 1e-9)

	v, err := enc.Decode([]float64{0.3})
	require.NoError(t, err)
	assert.InDelta(t, 3.0, parseFloatEnc(v["x"]), 1e-9)
}

func TestEncodeDecode_ContinuousClamp(t *testing.T) {
	space := ParamSpace{Params: []ParamDef{
		{Name: "x", Type: TypeContinuous, Min: 0, Max: 1},
	}}
	enc, _ := NewEncoder(space)
	u, _ := enc.Encode(ParamVector{"x": "5"}) // out of range → clamp
	assert.InDelta(t, 1.0, u[0], 1e-9)
	v, _ := enc.Decode([]float64{-1})
	assert.InDelta(t, 0.0, parseFloatEnc(v["x"]), 1e-9)
}

func TestEncodeDecode_Discrete(t *testing.T) {
	space := ParamSpace{Params: []ParamDef{
		{Name: "w", Type: TypeDiscrete, Values: []string{"1", "2", "4", "8"}},
	}}
	enc, _ := NewEncoder(space)

	for i, val := range []string{"1", "2", "4", "8"} {
		u, _ := enc.Encode(ParamVector{"w": val})
		want := float64(i) / float64(3)
		assert.InDelta(t, want, u[0], 1e-9, "level %s", val)
	}
	v0, _ := enc.Decode([]float64{0})
	assert.Equal(t, "1", v0["w"])
	v1, _ := enc.Decode([]float64{1})
	assert.Equal(t, "8", v1["w"])
}

func TestEncodeDecode_Categorical(t *testing.T) {
	space := ParamSpace{Params: []ParamDef{
		{Name: "cc", Type: TypeCategorical, Values: []string{"cubic", "bbr", "reno"}},
	}}
	enc, _ := NewEncoder(space)
	u, _ := enc.Encode(ParamVector{"cc": "reno"})
	assert.InDelta(t, 1.0, u[0], 1e-9)
	v, _ := enc.Decode([]float64{0.5})
	assert.Equal(t, "bbr", v["cc"])
}

func parseFloatEnc(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
