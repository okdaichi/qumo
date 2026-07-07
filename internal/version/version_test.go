package version

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionFunctions(t *testing.T) {
	// Save original values to restore them later
	origVersion := version
	origCommit := commit
	origDate := date

	defer func() {
		version = origVersion
		commit = origCommit
		date = origDate
	}()

	t.Run("DefaultValues", func(t *testing.T) {
		assert.Equal(t, "dev", Version())
		assert.Equal(t, "none", Commit())
		assert.Equal(t, "unknown", Date())

		full := Full()
		assert.True(t, strings.Contains(full, "qumo dev"))
		assert.True(t, strings.Contains(full, "commit: none"))
		assert.True(t, strings.Contains(full, "built:  unknown"))

		assert.Equal(t, "qumo dev", Short())
	})

	t.Run("ModifiedValues", func(t *testing.T) {
		// Modify values to simulate ldflags injection
		version = "v1.2.3"
		commit = "abcdef1"
		date = "2023-01-01T00:00:00Z"

		assert.Equal(t, "v1.2.3", Version())
		assert.Equal(t, "abcdef1", Commit())
		assert.Equal(t, "2023-01-01T00:00:00Z", Date())

		full := Full()
		assert.True(t, strings.Contains(full, "qumo v1.2.3"))
		assert.True(t, strings.Contains(full, "commit: abcdef1"))
		assert.True(t, strings.Contains(full, "built:  2023-01-01T00:00:00Z"))

		assert.Equal(t, "qumo v1.2.3", Short())
	})
}
