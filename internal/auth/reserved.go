// ReservedProjectRoots are the mirror prefixes: oci.{domain}/v2/<root> and dl.{domain}/github.

package auth

import (
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// ReservedProjectRoots are the mirror prefixes: oci.{domain}/v2/<root> and dl.{domain}/github.
var ReservedProjectRoots = set.Of("dockerhub", "ghcr", "github")

// ReservedProjectName reports whether name sits under a reserved root.
func ReservedProjectName(name string) bool {
	root, _, _ := strings.Cut(name, "/")
	return ReservedProjectRoots.Contains(root)
}
