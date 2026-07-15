// BayesianScheduler (Stage 2) decides where to measure next from the GP
// posterior: round 0 is an LHS seed batch (broad coverage); each later round
// fits the GP and picks the next point(s) by maximizing an acquisition function
// (Expected Improvement / UCB / predictive variance). This replaces the
// neighbor-of-best hill-climb of StaticScheduler with uncertainty-driven search.

package sampler

import (
	"context"
	"fmt"
	"io"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/model"
)

// BayesianScheduler drives uncertainty-driven exploration.
type BayesianScheduler struct {
	// LHSn is the round-0 Latin-Hypercube seed batch (Stage 1 broad coverage).
	LHSn int
	// Rounds is the number of acquisition-driven rounds after the seed batch.
	Rounds int
	// Batch is the number of points selected per round (greedy, exclusion-based
	// diversification — full Kriging-believer is deferred). 1 = pure sequential.
	Batch int
	// Acquisition selects the acquisition: "ei" (default), "ucb", or "variance".
	Acquisition string
	// Kappa (UCB exploration) and Xi (EI exploration beyond best).
	Kappa, Xi float64
	// Candidates is the random-search budget for acquisition maximization.
	Candidates int
	// GPStarts is the GP multistart budget per fit (0 → model default).
	GPStarts int
	// Seed makes a run reproducible.
	Seed uint64

	round int
}

// Next returns the next batch of vectors.
func (s *BayesianScheduler) Next(ctx context.Context, st SchedulerState) ([]experiment.ParamVector, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	// Round 0: broad LHS seed.
	if s.round == 0 {
		s.round++
		n := s.LHSn
		if n < 1 {
			n = 1
		}
		vectors, err := LHS{Seed: s.Seed}.Sample(st.Enc, n)
		if err != nil {
			return nil, "", fmt.Errorf("bo lhs seed: %w", err)
		}
		return vectors, "lhs", nil
	}

	if s.round > s.Rounds {
		return nil, "", io.EOF
	}
	// Need enough points to fit a GP; if not yet, signal done (the seed batch
	// may simply have been too small).
	if len(st.Observations) < 4 || st.Enc == nil {
		return nil, "", io.EOF
	}

	gp, err := model.FitGP(st.Observations, st.Objective, model.Options{Starts: s.GPStarts, Seed: s.Seed})
	if err != nil {
		return nil, "", fmt.Errorf("bo fit: %w", err)
	}

	best := bestObjective(st.Observations, st.Objective)
	acq := model.AcquisitionFor(s.Acquisition, best, s.Xi, s.Kappa)

	batch := s.Batch
	if batch < 1 {
		batch = 1
	}

	// Exclude already-measured vectors; SelectByAcquisition enumerates the
	// discrete space (or random-searches continuous) and returns novel picks.
	excludeVecs := make([]experiment.ParamVector, len(st.Observations))
	for i, o := range st.Observations {
		excludeVecs[i] = o.Vector
	}
	rng := model.NewLCG(s.Seed ^ uint64(s.round)*0x9e3779b97f4a7c15)
	picks := model.SelectByAcquisition(gp, acq, st.Enc, st.Space, excludeVecs, batch, rng)

	phase := fmt.Sprintf("bo-%d", s.round)
	s.round++
	if len(picks) == 0 {
		return nil, "", io.EOF // no novel points remain → done
	}
	return picks, phase, nil
}

// bestObjective returns the max objective mean across observations.
func bestObjective(obs []experiment.Observation, objective string) float64 {
	best := 0.0
	first := true
	for _, o := range obs {
		v, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		if first || v > best {
			best = v
			first = false
		}
	}
	return best
}

func observedHas(obs []experiment.Observation, v experiment.ParamVector) bool {
	for _, o := range obs {
		if o.Vector.Equal(v) {
			return true
		}
	}
	return false
}
