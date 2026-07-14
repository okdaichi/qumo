// Encoder bridges the string-valued parameter space the black-box runner
// consumes and the normalized [0,1]^D numeric space that samplers and surrogate
// models operate in.
//
// It is the single source of truth for that mapping: samplers produce points in
// [0,1]^D and the scheduler decodes them to string ParamVectors before the
// runner is invoked, so the benchmark script never sees encoded numbers.

package experiment

import (
	"fmt"
	"math"
	"strconv"
)

// Encoder encodes/decodes parameter vectors for a fixed ParamSpace.
type Encoder struct {
	specs []encSpec
}

type encSpec struct {
	name     string
	typ      ParamType
	values   []string // discrete/categorical levels (declared order)
	min, max float64  // continuous bounds
}

// NewEncoder builds an Encoder from a (normalized) ParamSpace.
func NewEncoder(space ParamSpace) (*Encoder, error) {
	e := &Encoder{specs: make([]encSpec, len(space.Params))}
	for i, p := range space.Params {
		s := encSpec{name: p.Name, typ: p.Type, values: p.Values, min: p.Min, max: p.Max}
		switch p.Type {
		case TypeContinuous:
			if !(p.Min < p.Max) {
				return nil, fmt.Errorf("encoder: continuous param %s needs min < max", p.Name)
			}
		case TypeDiscrete, TypeCategorical:
			if len(p.Values) < 2 {
				return nil, fmt.Errorf("encoder: param %s needs ≥2 values", p.Name)
			}
		default:
			return nil, fmt.Errorf("encoder: param %s has unspecified type (call ParamSpace.Normalize first)", p.Name)
		}
		e.specs[i] = s
	}
	return e, nil
}

// Dim returns the dimensionality of the encoded space (one coordinate per param).
func (e *Encoder) Dim() int { return len(e.specs) }

// Names returns the parameter names in declared order.
func (e *Encoder) Names() []string {
	out := make([]string, len(e.specs))
	for i, s := range e.specs {
		out[i] = s.name
	}
	return out
}

// Encode maps a string ParamVector to a normalized [0,1]^D vector.
func (e *Encoder) Encode(v ParamVector) ([]float64, error) {
	out := make([]float64, len(e.specs))
	for i, s := range e.specs {
		val, ok := v[s.name]
		if !ok {
			return nil, fmt.Errorf("encode: missing param %s", s.name)
		}
		u, err := s.encode(val)
		if err != nil {
			return nil, fmt.Errorf("encode %s=%q: %w", s.name, val, err)
		}
		out[i] = u
	}
	return out, nil
}

// Decode maps a normalized [0,1]^D vector back to a string ParamVector.
func (e *Encoder) Decode(x []float64) (ParamVector, error) {
	if len(x) != len(e.specs) {
		return nil, fmt.Errorf("decode: expected %d dims, got %d", len(e.specs), len(x))
	}
	out := make(ParamVector, len(e.specs))
	for i, s := range e.specs {
		out[s.name] = s.decode(clampUnit(x[i]))
	}
	return out, nil
}

func (s *encSpec) encode(val string) (float64, error) {
	switch s.typ {
	case TypeContinuous:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, err
		}
		return clampUnit((f - s.min) / (s.max - s.min)), nil
	case TypeDiscrete, TypeCategorical:
		idx := indexOfString(s.values, val)
		if idx < 0 {
			return 0, fmt.Errorf("value not in declared levels")
		}
		if len(s.values) == 1 {
			return 0, nil
		}
		return float64(idx) / float64(len(s.values)-1), nil
	}
	return 0, fmt.Errorf("unsupported type")
}

func (s *encSpec) decode(u float64) string {
	switch s.typ {
	case TypeContinuous:
		f := s.min + u*(s.max-s.min)
		return strconv.FormatFloat(f, 'g', -1, 64)
	case TypeDiscrete, TypeCategorical:
		if len(s.values) == 1 {
			return s.values[0]
		}
		idx := int(math.Round(u * float64(len(s.values)-1)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(s.values) {
			idx = len(s.values) - 1
		}
		return s.values[idx]
	}
	return ""
}

func clampUnit(u float64) float64 {
	if u < 0 {
		return 0
	}
	if u > 1 {
		return 1
	}
	if math.IsNaN(u) {
		return 0
	}
	return u
}

func indexOfString(values []string, v string) int {
	for i, x := range values {
		if x == v {
			return i
		}
	}
	return -1
}
