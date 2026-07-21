package doctor

import (
	"bytes"
	"testing"

	"github.com/qumo-dev/qumo/internal/gctune"
	"github.com/stretchr/testify/assert"
)

func TestReport(t *testing.T) {
	tests := map[string]struct {
		env         gctune.Env
		wantContain []string
	}{
		"nothing set → runtime default, opt-in hint": {
			env:         gctune.Env{},
			wantContain: []string{"GOGC        = (unset)", "RELAY_GOGC  = (unset)", "100%", "source: runtime default", "Set RELAY_GOGC"},
		},
		"RELAY_GOGC applied → shows value and source": {
			env:         gctune.Env{RelayGOGC: "800"},
			wantContain: []string{"RELAY_GOGC  = 800", "800%", "source: RELAY_GOGC"},
		},
		"GOGC owns it → runtime-controlled, no relay override": {
			env:         gctune.Env{GOGC: "150", RelayGOGC: "800"},
			wantContain: []string{"GOGC=150 (runtime-controlled)", "source: GOGC", "never overrides"},
		},
		"invalid RELAY_GOGC → warning + falls back to default": {
			env:         gctune.Env{RelayGOGC: "notanint"},
			wantContain: []string{"Warning:", "invalid RELAY_GOGC", "100%", "source: runtime default"},
		},
		"GOMEMLIMIT set → discouraged warning": {
			env:         gctune.Env{RelayGOGC: "800", GOMEMLIMIT: "8GiB"},
			wantContain: []string{"GOMEMLIMIT  = 8GiB", "GOMEMLIMIT is set (8GiB); not recommended"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			report(&buf, tt.env)
			out := buf.String()
			for _, want := range tt.wantContain {
				assert.Contains(t, out, want)
			}
		})
	}
}
