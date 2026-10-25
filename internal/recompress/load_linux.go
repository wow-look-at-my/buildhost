//go:build linux

package recompress

// hostLoad1 reads the one-minute load average from procfs. A Cosmopolitan
// build satisfies the linux tag on every platform it runs on, so a host with
// no procfs is asked through its own sysctl program instead.
func hostLoad1() float64 {
	if v, ok := readProcLoadavg(); ok {
		return v
	}
	return sysctlLoadavg()
}
