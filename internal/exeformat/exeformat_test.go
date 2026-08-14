package exeformat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetect(t *testing.T) {
	for name, tc := range map[string]struct {
		head []byte
		want Format
	}{
		"ape":            {[]byte("MZqFpD\x00\x00rest"), APE},
		"ape exact len":  {[]byte("MZqFpD"), APE},
		"elf":            {[]byte("\x7fELF\x02\x01"), ""},
		"macho":          {[]byte("\xcf\xfa\xed\xfe\x0c\x00"), ""},
		"bare pe":        {[]byte("MZ\x90\x00\x03\x00"), ""},
		"short":          {[]byte("MZqFp"), ""},
		"empty":          {nil, ""},
		"magic not at 0": {[]byte("\x00MZqFpD"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Detect(tc.head))
		})
	}
}

func TestFormatLabelAndCapability(t *testing.T) {
	assert.Equal(t, "APE", APE.Label())
	assert.True(t, APE.MultiPlatformCapable())
	assert.Empty(t, Format("").Label())
	assert.False(t, Format("").MultiPlatformCapable())
	assert.False(t, Format("elf").MultiPlatformCapable())
}
