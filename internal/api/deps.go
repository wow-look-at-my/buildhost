package api

// Keeps go.mod requiring the generated validators' runtime through the tidy
// that a fresh clone runs before generate. Untagged: a tagged file is
// unreachable to the build check.
import _ "github.com/wow-look-at-my/go-regex-compiler/match"
