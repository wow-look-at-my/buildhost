//go:build !windows

package admin

import (
	"syscall"
	"time"
)

func getCPUTime() time.Duration {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano()) + time.Duration(usage.Stime.Nano())
}
