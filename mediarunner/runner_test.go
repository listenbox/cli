package mediarunner

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestExecutableTimeoutRejectsUnexpectedBinary(t *testing.T) {
	t.Parallel()

	_, err := executableTimeout("sh")
	assert.ErrorContains(t, err, `unsupported media executable "sh"`)
}

func TestCappedBufferBoundsOutput(t *testing.T) {
	t.Parallel()

	buffer := newCappedBuffer(4)
	written, err := buffer.Write([]byte("abcdef"))
	assert.NilError(t, err)
	assert.Equal(t, written, 6)
	assert.Equal(t, string(buffer.Bytes()), "abcd")
	assert.Assert(t, buffer.overflowed)
}
