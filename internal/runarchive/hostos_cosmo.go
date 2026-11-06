//go:build cosmo

// hostos_cosmo.go holds the hostos cosmo code.

package runarchive

import "runtime"

// hostOS asks the runtime where the APE landed: an APE reports GOOS "cosmo" on every host.
var hostOS = runtime.CosmoHostOS
