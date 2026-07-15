// Suggested-next measurements: after exploration, the top acquisition points are
// the framework's answer to "what should we measure next?" (a brief ask). These
// are the points where the model is most uncertain/promising, NOT yet measured.

package analysis

import (
	"sort"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/model"
)

// Suggestion is a recommended next measurement: where to sample, why (acquisition
// value), and what the model currently predicts there.
type Suggestion struct {
	Vector        experiment.ParamVector `json:"vector"`
	EncodedX      []float64              `json:"encoded_x"`
	AcqValue      float64                `json:"acq_value"`
	PredictedMean float64                `json:"predicted_mean"`
	PredictedStd  float64                `json:"predicted_std"`
}

// SuggestedNext returns up to n distinct points maximizing acq that have not
// been measured yet — the model's recommendation for where to sample next. The
// caller constructs acq (e.g. via model.AcquisitionFor) with the current best.
func SuggestedNext(gp *model.GaussianProcess, enc *experiment.Encoder, acq model.Acquisition, n int, seed uint64) []Suggestion {
	if gp == nil || enc == nil || n <= 0 {
		return nil
	}
	dim := enc.Dim()
	candidates := dim * 200
	rng := model.NewLCG(seed)
	exclude := make([][]float64, 0, n)
	out := make([]Suggestion, 0, n)
	for len(out) < n {
		x, val := model.MaximizeAcquisition(gp, acq, dim, candidates, rng, exclude)
		if x == nil {
			break
		}
		vec, err := enc.Decode(x)
		if err != nil {
			exclude = append(exclude, x)
			continue
		}
		mean, std, _ := gp.Predict(x)
		out = append(out, Suggestion{
			Vector: vec, EncodedX: x, AcqValue: val,
			PredictedMean: mean, PredictedStd: std,
		})
		exclude = append(exclude, x)
	}
	// Rank by acquisition value (each pick is an independent random-search argmax,
	// so consecutive values aren't inherently monotone — sort for output).
	sort.Slice(out, func(i, j int) bool { return out[i].AcqValue > out[j].AcqValue })
	return out
}
