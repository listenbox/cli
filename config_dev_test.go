//go:build dev

package main

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestEmbeddedCLIConfigDefaultsToDevelopment(t *testing.T) {
	t.Parallel()
	config, err := loadEmbeddedCLIConfig()
	assert.NilError(t, err)
	assert.Equal(t, config.apiOrigin, "http://localhost:8080")
	assert.Equal(t, config.dashboardOrigin, "http://localhost:5174")
	assert.Equal(t, config.printTraceIDs, true)
}
