package repackage

import _ "embed"

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/template"
)

var apeMagic = []byte("MZqFpD='")

// peekAPE reports whether r begins with the APE prologue, returning a reader
// that still yields the full stream. Detection is a byte comparison: buildhost
// never executes an uploaded artifact to learn what it is.
func peekAPE(r io.Reader) (bool, io.Reader, error) {
	head := make([]byte, len(apeMagic))
	n, err := io.ReadFull(r, head)
	head = head[:n]
	rest := io.MultiReader(bytes.NewReader(head), r)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, rest, err
	}
	return bytes.Equal(head, apeMagic), rest, nil
}

// debAPELauncherTarget is where the package installs the APE itself, out of the
// way of the /usr/bin entry that starts it.
func debAPELauncherTarget(pkgName string) string {
	return fmt.Sprintf("/usr/lib/%s/%s", pkgName, pkgName)
}

// debAPELauncherScript renders the /usr/bin entry. Real is already
// shell-quoted.
var debAPELauncherScript = template.Must(template.New("deb-ape-launcher").Parse(
	debAPELauncherScriptSh))

// debAPELauncher is the /usr/bin entry generated for an APE binary.
func debAPELauncher(pkgName, version string) (string, error) {
	var b strings.Builder
	err := debAPELauncherScript.Execute(&b, struct{ Pkg, Version, Real string }{
		Pkg:     pkgName,
		Version: version,
		Real:    shellQuote(debAPELauncherTarget(pkgName)),
	})
	if err != nil {
		return "", fmt.Errorf("render the deb APE launcher for %s: %w", pkgName, err)
	}
	return b.String(), nil
}

// shellQuote renders s as a single-quoted shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

//go:embed deb-ape-launcher-script.sh
var debAPELauncherScriptSh string
