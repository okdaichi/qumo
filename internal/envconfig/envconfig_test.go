package envconfig

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestString(t *testing.T) {
	// Use a unique key so a stray value in the ambient environment cannot affect
	// the "unset → default" case.
	key := "QUMO_TEST_ENVCONFIG_STRING"

	os.Unsetenv(key)
	assert.Equal(t, "default", String(key, "default"), "unset → default")

	t.Setenv(key, "actual")
	assert.Equal(t, "actual", String(key, "default"), "set → value")

	t.Setenv(key, "")
	assert.Equal(t, "default", String(key, "default"), "empty → default (empty is treated as unset)")
}
