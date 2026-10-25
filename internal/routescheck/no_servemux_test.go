package routescheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// bannedHTTP are the net/http names of the standard library's request router.
var bannedHTTP = set.Of("ServeMux", "NewServeMux", "DefaultServeMux", "Handle", "HandleFunc")

func TestNoStandardLibraryRouter(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		files++
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		name := httpImportName(f)
		if name == "" {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == name && bannedHTTP.Contains(sel.Sel.Name) {
				assert.Fail(t, "the standard library router is not used here",
					"%s: http.%s. Use github.com/wow-look-at-my/router: router.New(), then HandleFunc(pattern, router.Allow, handler).",
					fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, files, 100, "the walk must reach the repository's Go files")
}

// httpImportName is the name a file imports net/http as, or "" when it does not.
func httpImportName(f *ast.File) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != "net/http" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "http"
	}
	return ""
}
