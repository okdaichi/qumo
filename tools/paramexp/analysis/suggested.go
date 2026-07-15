// Suggested-next measurements: after exploration, the top acquisition points are
// the framework's answer to "what should we measure next?" (a brief ask). These
// are the points where the model is most uncertain/promising, NOT yet measured.

package analysis

import (
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

// SuggestedNext returns up to n unmeasured points maximizing acq. Observed
// vectors are excluded; for discrete spaces all candidates are enumerated
// (guaranteed distinct + novel). The caller constructs acq (e.g. via
// model.AcquisitionFor) with the current best.
func SuggestedNext(gp *model.GaussianProcess, enc *experiment.Encoder, space experiment.ParamSpace,
	acq model.Acquisition, observed []experiment.ParamVector, n int, seed uint64) []Suggestion {
	if gp == nil || enc == nil || n <= 0 {
		return nil
	}
	rng := model.NewLCG(seed)
	picks := model.SelectByAcquisition(gp, acq, enc, space, observed, n, rng)

	out := make([]Suggestion, 0, len(picks))
	for _, vec := range picks {
		x, err := enc.Encode(vec)
		if err != nil {
			continue
		}
		mean, std, _ := gp.Predict(x)
		out = append(out, Suggestion{
			Vector: vec, EncodedX: x, AcqValue: acq(gp, x),
			PredictedMean: mean, PredictedStd: std,
		})
	}
	return out
}
