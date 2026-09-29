package repackage

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func debControl(t *testing.T, input Input) string {
	t.Helper()
	output, err := (&Deb{}).Repackage(context.Background(), input)
	require.NoError(t, err)
	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())
	control, ok := tarGzEntries(t, readArMembers(t, data)["control.tar.gz"])["./control"]
	require.True(t, ok, "the deb must carry ./control")
	return control
}

func TestDebRepackage_ControlCarriesDepends(t *testing.T) {
	t.Serial()
	input := makeInput()
	input.Project.AptDepends = "bubblewrap | docker.io"

	control := debControl(t, input)
	assert.Contains(t, control, "\nDepends: bubblewrap | docker.io\n")
	assert.Contains(t, control, "Package: testapp\n")
}

func TestDebRepackage_ControlWithoutDependsHasNoLine(t *testing.T) {
	t.Serial()
	assert.NotContains(t, debControl(t, makeInput()), "Depends:")
}

func TestDebRepackage_InvalidStoredDependsFails(t *testing.T) {
	t.Serial()
	input := makeInput()
	input.Project.AptDepends = "bubblewrap\nEssential: yes"

	_, err := (&Deb{}).Repackage(context.Background(), input)
	require.Error(t, err, "a value that would open another control field must fail the build of the deb")
}
