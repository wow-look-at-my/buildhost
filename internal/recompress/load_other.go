//go:build !linux && !darwin

package recompress

// hostLoad1 reads the one-minute load average from procfs where it exists, and
// from the host's sysctl program otherwise.
func hostLoad1() float64 {
	if v, ok := readProcLoadavg(); ok {
		return v
	}
	return sysctlLoadavg()
}
