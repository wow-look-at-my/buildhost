// marker is the entrypoint of the image the OCI round-trip test PUSHES to
// buildhost. It prints a fixed string and exits, so a `docker run` of the image
// pulled back out proves the bytes survived the push, the storage and the pull.
//
// It reads nothing and dials nothing: a failure here is buildhost's, never the
// runner's network or a missing CA bundle.
package main

import "fmt"

func main() {
	fmt.Println("MARKER-OK")
}
