package ingest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvOr(t *testing.T) {
	key := "TEST_ENV_OR"
	defaultVal := "default"

	// Test default value
	os.Unsetenv(key)
	assert.Equal(t, defaultVal, envOr(key, defaultVal))

	// Test env value
	expected := "actual"
	t.Setenv(key, expected)
	assert.Equal(t, expected, envOr(key, defaultVal))
}
