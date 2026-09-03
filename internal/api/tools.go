//go:build tools

package api

// The generated validators are gitignored, so this is what keeps go.mod requiring their runtime.
import _ "github.com/wow-look-at-my/go-regex-compiler/match"
