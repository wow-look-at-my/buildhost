package exeformat

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// apeWithPESections builds a minimal APE whose PE header, reached through the
// DOS header's e_lfanew pointer, declares nsect sections. peOff mirrors the
// 0x80 a gosmopolitan APE uses; a caller can move it to test the sniff window.
func apeWithPESections(t *testing.T, peOff int, nsect uint16) []byte {
	t.Helper()
	b := make([]byte, peOff+8)
	copy(b, "MZqFpD")
	binary.LittleEndian.PutUint32(b[elfanewOff:], uint32(peOff))
	binary.LittleEndian.PutUint16(b[peOff+peNsectOff:], nsect)
	return b
}

func TestDetectNTBoot(t *testing.T) {
	for name, tc := range map[string]struct {
		head []byte
		want NTBoot
	}{
		"stub, one section":    {apeWithPESections(t, 0x80, 1), NTStub},
		"real, three sections": {apeWithPESections(t, 0x80, 3), NTReal},
		"real, two sections":   {apeWithPESections(t, 0x80, 2), NTReal},
		"real, many sections":  {apeWithPESections(t, 0x80, 9), NTReal},
		// Not an APE at all: this check has nothing to say about it.
		"elf":      {[]byte("\x7fELF\x02\x01"), NTUnknown},
		"bare pe":  {[]byte("MZ\x90\x00\x03\x00"), NTUnknown},
		"empty":    {nil, NTUnknown},
		"ape only": {[]byte("MZqFpD"), NTUnknown},
		// A PE header past the sniff window is unknown, never a rejection: the
		"pe header past sniff window": {apeWithPESections(t, SniffLen*4, 1)[:SniffLen], NTUnknown},
		// A truncated DOS header cannot even be followed.
		"truncated before e_lfanew": {apeWithPESections(t, 0x80, 1)[:elfanewOff+2], NTUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, DetectNTBoot(tc.head))
		})
	}
}

// TestDetectNTBootOnRealAPE pins the check against the layout a real
func TestDetectNTBootOnRealAPE(t *testing.T) {
	head := apeWithPESections(t, 0x80, 3)
	require.Equal(t, uint32(0x80), binary.LittleEndian.Uint32(head[0x3c:]))
	require.Equal(t, uint16(3), binary.LittleEndian.Uint16(head[0x86:]))
	assert.Equal(t, NTReal, DetectNTBoot(head))
	assert.LessOrEqual(t, 0x88, SniffLen, "the sniff window must reach a real APE's section count")
}
