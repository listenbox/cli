package main

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/goleak"
	"gotest.tools/v3/assert"
)

func TestWriteCLIErrorPrintsOnlyValidationMessages(t *testing.T) {
	t.Parallel()

	err := runCLIWithHome(
		context.Background(),
		[]string{showsCommandName, createCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, "--type is required")

	var stderr bytes.Buffer
	writeCLIError(&stderr, err)

	assert.Equal(t, stderr.String(), "shows create: invalid arguments\n"+
		"  --title is required\n"+
		"  --slug is required\n"+
		"  --type is required\n"+
		"  --language is required\n")
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
