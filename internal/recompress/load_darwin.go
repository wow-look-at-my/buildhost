//go:build darwin

package recompress

import (
	"encoding/binary"
	"syscall"
)

// hostLoad1 reads the one-minute load average from vm.loadavg. The kernel
// answers with struct loadavg: three fixed-point averages followed, after the
// alignment padding of a 64-bit long, by the scale they are divided by. The
// averages are 2048ths of a runnable thread.
func hostLoad1() float64 {
	raw, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0
	}
	b := []byte(raw)
	if len(b) < 20 {
		return 0
	}
	load := binary.LittleEndian.Uint32(b[0:4])
	scale := binary.LittleEndian.Uint32(b[16:20])
	if scale == 0 {
		return 0
	}
	return float64(load) / float64(scale)
}
