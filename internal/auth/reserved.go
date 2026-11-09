// ReservedProjectRoots are the registry mirror's prefixes on oci.{domain}/v2/.

package auth

import (
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// ReservedProjectRoots are the registry mirror's prefixes on oci.{domain}/v2/.
var ReservedProjectRoots = set.Of("dockerhub", "ghcr")

// ReservedProjectName reports whether name sits under a reserved root.
func ReservedProjectName(name string) bool {
	root, _, _ := strings.Cut(name, "/")
	return ReservedProjectRoots.Contains(root)
}
