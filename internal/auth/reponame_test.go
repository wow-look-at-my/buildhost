package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenamedNamespaceName(t *testing.T) {
	t.Serial()
	cases := []struct {
		name string
		from string
		root string
		want string
	}{
		{"root project follows the repo", "slopfmt", "slopfix", "slopfix"},
		{"child keeps its own segment", "slopfmt/probe", "slopfix", "slopfix/probe"},
		{"deep child keeps its path", "old/a/b", "new", "new/a/b"},
		{"already correct", "slopfix", "slopfix", ""},
		{"child already correct", "slopfix/probe", "slopfix", ""},
		// The child repeats the root, so the root already names it.
		{"child repeating the root collapses", "log-progress-indicator/lpi", "lpi", "lpi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, renamedNamespaceName(tc.from, tc.root))
		})
	}
}

func TestRepoNamespaceRoot(t *testing.T) {
	t.Serial()
	assert.Equal(t, "buildhost", repoNamespaceRoot("wow-look-at-my/buildhost"))
	assert.Equal(t, "mixedcase", repoNamespaceRoot("wow-look-at-my/MixedCase"))
	assert.Equal(t, "", repoNamespaceRoot("no-slash"))
	assert.Equal(t, "", repoNamespaceRoot("trailing/"))
}
