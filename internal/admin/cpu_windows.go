//go:build windows

package admin

import "time"

func getCPUTime() time.Duration {
	return 0
}
