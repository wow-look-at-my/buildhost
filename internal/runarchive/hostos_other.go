//go:build !cosmo

package runarchive

import "runtime"

// hostOS is the OS this binary was built for, which is the host it runs on for every target except cosmo.
var hostOS = func() string { return runtime.GOOS }
