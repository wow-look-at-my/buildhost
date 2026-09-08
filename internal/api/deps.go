package api

// The generated validators are gitignored, so a fresh clone runs `go mod tidy`
// before they exist. This blank import is what keeps go.mod requiring their
// runtime across that tidy. It carries no build tag on purpose: a tagged file
// is unreachable to the build-check pipeline, which fails the build.
import _ "github.com/wow-look-at-my/go-regex-compiler/match"
