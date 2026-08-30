//go:build !dev

package cli

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestEmbeddedCLIConfigDefaultsToProduction(t *testing.T) {
	t.Parallel()
	config, err := loadEmbeddedCLIConfig()
	assert.NilError(t, err)
	assert.Equal(t, config.apiOrigin, "https://v1.listenbox.app")
	assert.Equal(t, config.dashboardOrigin, "https://web.listenbox.app")
	assert.Equal(t, config.printTraceIDs, false)
}
