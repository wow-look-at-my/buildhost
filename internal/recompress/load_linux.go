//go:build linux

// hostLoad1 reads the one-minute load average from procfs.

package recompress

// hostLoad1 reads the one-minute load average from procfs.
func hostLoad1() float64 {
	if v, ok := readProcLoadavg(); ok {
		return v
	}
	return sysctlLoadavg()
}
