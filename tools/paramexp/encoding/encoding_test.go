package encoding

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

func TestEncodeDecode_Continuous(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "x", Type: experiment.TypeContinuous, Min: 0, Max: 10},
	}}
	enc, err := New(space)
	require.NoError(t, err)

	u, err := enc.Encode(experiment.ParamVector{"x": "5"})
	require.NoError(t, err)
	assert.InDelta(t, 0.5, u[0], 1e-9)

	v, err := enc.Decode([]float64{0.3})
	require.NoError(t, err)
	assert.InDelta(t, 3.0, parseFloat(v["x"]), 1e-9)
}

func TestEncodeDecode_ContinuousClamp(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "x", Type: experiment.TypeContinuous, Min: 0, Max: 1},
	}}
	enc, _ := New(space)
	u, _ := enc.Encode(experiment.ParamVector{"x": "5"}) // out of range → clamp
	assert.InDelta(t, 1.0, u[0], 1e-9)
	v, _ := enc.Decode([]float64{-1})
	assert.InDelta(t, 0.0, parseFloat(v["x"]), 1e-9)
}

func TestEncodeDecode_Discrete(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8"}},
	}}
	enc, _ := New(space)

	// Encode each level and check monotonic mapping into [0,1].
	for i, val := range []string{"1", "2", "4", "8"} {
		u, _ := enc.Encode(experiment.ParamVector{"w": val})
		want := float64(i) / float64(3)
		assert.InDelta(t, want, u[0], 1e-9, "level %s", val)
	}
	// Decode 0 and 1 to the endpoints.
	v0, _ := enc.Decode([]float64{0})
	assert.Equal(t, "1", v0["w"])
	v1, _ := enc.Decode([]float64{1})
	assert.Equal(t, "8", v1["w"])
}

func TestEncodeDecode_Categorical(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "cc", Type: experiment.TypeCategorical, Values: []string{"cubic", "bbr", "reno"}},
	}}
	enc, _ := New(space)
	u, _ := enc.Encode(experiment.ParamVector{"cc": "reno"})
	assert.InDelta(t, 1.0, u[0], 1e-9)
	v, _ := enc.Decode([]float64{0.5})
	assert.Equal(t, "bbr", v["cc"])
}

func TestTypeInference(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "num", Values: []string{"1", "2", "4"}},      // all numeric → discrete
		{Name: "mix", Values: []string{"16KB", "64KB"}},     // non-numeric → categorical
		{Name: "c", Min: 1, Max: 9},                         // has bounds → continuous
	}}
	require.NoError(t, space.Normalize())
	assert.Equal(t, experiment.TypeDiscrete, space.Params[0].Type)
	assert.Equal(t, experiment.TypeCategorical, space.Params[1].Type)
	assert.Equal(t, experiment.TypeContinuous, space.Params[2].Type)
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
