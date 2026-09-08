package repackage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// testELFHeader is a 64-byte ELF64 header for machine.
func testELFHeader(machine uint16) []byte {
	h := make([]byte, apeELFHeaderSize)
	copy(h, "\x7fELF\x02\x01\x01\x00")
	binary.LittleEndian.PutUint16(h[16:], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(h[18:], machine)
	binary.LittleEndian.PutUint32(h[20:], 1) // EV_CURRENT
	return h
}

// testPrintfOctal renders b as the escaped body of a single-quoted printf word,
// which is how the trampoline spells a header byte.
func testPrintfOctal(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		fmt.Fprintf(&out, `\%03o`, c)
	}
	return out.String()
}

// testAPE builds a payload shaped like an APE: the prologue magic, a printf
// call per architecture, and a body. The trampoline uses those calls to turn
// its own copy into an ELF, and reading them back is how the image ships a
// binary the kernel can load.
func testAPE(machines ...uint16) []byte {
	var b strings.Builder
	b.WriteString("MZqFpD='\n")
	for _, m := range machines {
		b.WriteString("    printf '" + testPrintfOctal(testELFHeader(m)) + "' >&7\n")
	}
	b.WriteString(strings.Repeat("payload\n", 32))
	return []byte(b.String())
}

func TestAPEAsELFPicksTheHeaderForTheArch(t *testing.T) {
	t.Serial()
	payload := testAPE(0x3e, 0xb7) // EM_X86_64, EM_AARCH64

	for _, tc := range []struct {
		arch    db.Arch
		machine uint16
	}{
		{db.ArchAMD64, 0x3e},
		{db.ArchARM64, 0xb7},
	} {
		r, err := apeAsELF(bytes.NewReader(payload), tc.arch)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)

		assert.Len(t, got, len(payload), "patching the header must not change the length")
		assert.Equal(t, testELFHeader(tc.machine), got[:apeELFHeaderSize],
			"the image must carry the ELF header the trampoline would have written for %s", tc.arch)
		assert.Equal(t, payload[apeELFHeaderSize:], got[apeELFHeaderSize:],
			"everything past the prologue is the payload, untouched")
	}
}

// The prologue is the only place the header can come from, so a payload that
// carries none must fail rather than ship an image that cannot start.
func TestAPEAsELFRefusesAPayloadWithNoHeader(t *testing.T) {
	t.Serial()
	_, err := apeAsELF(strings.NewReader("MZqFpD='\nnothing here\n"), db.ArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ELF header")
}

// An APE must not be handed another architecture's header: the image would
// claim a platform whose binary cannot run.
func TestAPEAsELFRefusesAMissingArch(t *testing.T) {
	t.Serial()
	_, err := apeAsELF(bytes.NewReader(testAPE(0x3e)), db.ArchARM64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none is")
}

func TestAPEAsELFRefusesAnUnknownArch(t *testing.T) {
	t.Serial()
	_, err := apeAsELF(bytes.NewReader(testAPE(0x3e)), db.Arch386)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ELF machine is known")
}
