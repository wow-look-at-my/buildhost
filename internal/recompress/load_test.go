package recompress

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLoadavgText(t *testing.T) {
	t.Serial()
	assert.Equal(t, 5.44, parseLoadavgText("{ 5.44 8.50 5.88 }\n"))
	assert.Equal(t, 0.0, parseLoadavgText(""))
	assert.Equal(t, 0.0, parseLoadavgText("garbage"))
}

// The host this test runs on decides the value; the sample is logged so an
// unexpected zero on a busy machine is visible.
func TestHostLoadIsSampled(t *testing.T) {
	t.Serial()
	load := HostLoad()
	t.Logf("host load per CPU: %v", load)
	assert.GreaterOrEqual(t, load, 0.0)
	assert.LessOrEqual(t, load, 1000.0)
}
