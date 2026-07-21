package gctune_test

import (
	"testing"

	"github.com/qumo-dev/qumo/internal/gctune"
	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	tests := map[string]struct {
		env           gctune.Env
		wantApply     bool
		wantPercent   int
		wantSource    gctune.Source
		wantEffective int
		wantWarn      bool
	}{
		"nothing set → runtime default, no-op": {
			env:           gctune.Env{},
			wantApply:     false,
			wantSource:    gctune.SourceRuntimeDefault,
			wantEffective: 100,
		},
		"GOGC set → runtime owns it, no-op": {
			env:           gctune.Env{GOGC: "50"},
			wantApply:     false,
			wantSource:    gctune.SourceGOGC,
			wantEffective: -1,
		},
		"GOGC shadows RELAY_GOGC → no-op + warn (surfaced, not silent)": {
			env:           gctune.Env{GOGC: "50", RelayGOGC: "800"},
			wantApply:     false,
			wantSource:    gctune.SourceGOGC,
			wantEffective: -1,
			wantWarn:      true,
		},
		"RELAY_GOGC valid → apply it": {
			env:           gctune.Env{RelayGOGC: "800"},
			wantApply:     true,
			wantPercent:   800,
			wantSource:    gctune.SourceRelayGOGC,
			wantEffective: 800,
		},
		"RELAY_GOGC not an int → no-op + warn (opt-in)": {
			env:           gctune.Env{RelayGOGC: "notanint"},
			wantApply:     false,
			wantSource:    gctune.SourceRuntimeDefault,
			wantEffective: 100,
			wantWarn:      true,
		},
		"RELAY_GOGC zero → no-op + warn": {
			env:           gctune.Env{RelayGOGC: "0"},
			wantApply:     false,
			wantSource:    gctune.SourceRuntimeDefault,
			wantEffective: 100,
			wantWarn:      true,
		},
		"RELAY_GOGC negative → no-op + warn": {
			env:           gctune.Env{RelayGOGC: "-5"},
			wantApply:     false,
			wantSource:    gctune.SourceRuntimeDefault,
			wantEffective: 100,
			wantWarn:      true,
		},
		"GOMEMLIMIT is ignored by the percent policy": {
			env:           gctune.Env{RelayGOGC: "400", GOMEMLIMIT: "8GiB"},
			wantApply:     true,
			wantPercent:   400,
			wantSource:    gctune.SourceRelayGOGC,
			wantEffective: 400,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := gctune.Resolve(tt.env)
			assert.Equal(t, tt.wantApply, plan.Apply)
			assert.Equal(t, tt.wantSource, plan.Source)
			assert.Equal(t, tt.wantEffective, plan.EffectivePercent())
			assert.NotEmpty(t, plan.Reason, "every resolution must carry an operator-facing reason")
			if tt.wantApply {
				assert.Equal(t, tt.wantPercent, plan.Percent)
			}
			if tt.wantWarn {
				assert.NotEmpty(t, plan.Warnings)
			} else {
				assert.Empty(t, plan.Warnings)
			}
		})
	}
}
