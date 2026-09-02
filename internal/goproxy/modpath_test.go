package goproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseModulePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    []repoRef
		wantErr bool
	}{
		{
			name: "repo root",
			path: "github.com/wow-look-at-my/tml",
			want: []repoRef{{Owner: "wow-look-at-my", Repo: "tml"}},
		},
		{
			// A real shape in this org: the module is the "go" directory of the
			name: "nested module",
			path: "github.com/wow-look-at-my/agentic-loop/go",
			want: []repoRef{{Owner: "wow-look-at-my", Repo: "agentic-loop", Dir: "go"}},
		},
		{
			name: "deeply nested module",
			path: "github.com/o/r/a/b/c",
			want: []repoRef{{Owner: "o", Repo: "r", Dir: "a/b/c"}},
		},
		{
			// /v2 does not say where the code lives, so both layouts are candidates
			name: "major suffix at root or in a subdirectory",
			path: "github.com/o/r/v2",
			want: []repoRef{
				{Owner: "o", Repo: "r", Dir: "", Major: "v2"},
				{Owner: "o", Repo: "r", Dir: "v2", Major: "v2"},
			},
		},
		{
			name: "major suffix under a nested module",
			path: "github.com/o/r/sub/v3",
			want: []repoRef{
				{Owner: "o", Repo: "r", Dir: "sub", Major: "v3"},
				{Owner: "o", Repo: "r", Dir: "sub/v3", Major: "v3"},
			},
		},
		{
			// Go forbids a "/v1" suffix on a module path outright (v0 and v1 carry
			name:    "v1 suffix is not a legal module path",
			path:    "github.com/o/r/v1",
			wantErr: true,
		},
		{
			name: "double-digit major",
			path: "github.com/o/r/v12",
			want: []repoRef{
				{Owner: "o", Repo: "r", Dir: "", Major: "v12"},
				{Owner: "o", Repo: "r", Dir: "v12", Major: "v12"},
			},
		},
		{name: "not github", path: "golang.org/x/mod", wantErr: true},
		{name: "owner only", path: "github.com/wow-look-at-my", wantErr: true},
		{name: "not a module path", path: "github.com//r", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModulePath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				var e *Error
				require.ErrorAs(t, err, &e)
				assert.Equal(t, KindInvalidRequest, e.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTagPrefix(t *testing.T) {
	assert.Equal(t, "", repoRef{Owner: "o", Repo: "r"}.TagPrefix())
	assert.Equal(t, "go/", repoRef{Owner: "o", Repo: "r", Dir: "go"}.TagPrefix())
	assert.Equal(t, "a/b/", repoRef{Owner: "o", Repo: "r", Dir: "a/b"}.TagPrefix())
}

func TestMatchesPrefix(t *testing.T) {
	prefixes := []string{"github.com/wow-look-at-my", "github.com/PazerOP/"}

	assert.True(t, matchesPrefix("github.com/wow-look-at-my/tml", prefixes))
	assert.True(t, matchesPrefix("github.com/wow-look-at-my", prefixes))
	assert.True(t, matchesPrefix("github.com/wow-look-at-my/a/b/v2", prefixes))
	assert.True(t, matchesPrefix("github.com/PazerOP/thing", prefixes))

	// The boundary that matters: a prefix must not match a longer org name that
	assert.False(t, matchesPrefix("github.com/wow-look-at-my-evil/x", prefixes))
	assert.False(t, matchesPrefix("github.com/other/x", prefixes))
	assert.False(t, matchesPrefix("golang.org/x/mod", prefixes))
	assert.False(t, matchesPrefix("github.com/wow-look-at-my/tml", nil))
}
