// Scheduler decides which parameter vectors to run next, given the current set
// of observations. The phase-1 Static scheduler does an LHS batch followed by
// adaptive neighbor rounds; the same Scheduler interface will later be
// implemented by a Bayesian-optimization driver with no change to callers.

package sampler

import (
	"context"
	"fmt"
	"io"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// SchedulerState carries the information a Scheduler uses to choose the next batch.
type SchedulerState struct {
	Space        experiment.ParamSpace
	Enc          *experiment.Encoder
	Observations []experiment.Observation
	Objective    string
}

// Scheduler proposes the next batch of vectors. Returning io.EOF signals that
// the exploration budget is exhausted.
type Scheduler interface {
	Next(ctx context.Context, st SchedulerState) (vectors []experiment.ParamVector, phase string, err error)
}

// StaticScheduler drives a fixed two-phase exploration: an initial LHS sample
// batch, then a number of adaptive neighbor rounds around the best observations.
type StaticScheduler struct {
	LHSn           int // initial LHS sample count
	AdaptiveRounds int // number of adaptive neighbor rounds
	AdaptiveN      int // neighbors per round
	Seed           uint64

	round    int
	adaptive *Adaptive
}

// Next returns the next batch.
func (s *StaticScheduler) Next(ctx context.Context, st SchedulerState) ([]experiment.ParamVector, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if s.round == 0 {
		s.round++
		vectors, err := LHS{Seed: s.Seed}.Sample(st.Enc, s.LHSn)
		if err != nil {
			return nil, "", fmt.Errorf("lhs sample: %w", err)
		}
		return vectors, "lhs", nil
	}
	if s.round > s.AdaptiveRounds || len(st.Observations) < 3 {
		return nil, "", io.EOF
	}
	if s.adaptive == nil {
		s.adaptive = &Adaptive{}
	}
	neighbors, err := s.adaptive.SampleNear(st.Enc, st.Observations, s.AdaptiveN, st.Objective, st.Space)
	if err != nil {
		return nil, "", fmt.Errorf("adaptive sample: %w", err)
	}
	if len(neighbors) == 0 {
		return nil, "", io.EOF
	}
	phase := fmt.Sprintf("adaptive-%d", s.round)
	s.round++
	return neighbors, phase, nil
}
