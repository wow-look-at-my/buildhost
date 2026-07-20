package repackage

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// The deb materialization of the packaging-agnostic create_service setting:
// a flagged binary project's deb ships a systemd USER unit -- ordered
// after/bound to graphical-session.target (this is a per-user, often GUI,
// app) with the crash-only Restart=on-failure (the brew KeepAlive
// {SuccessfulExit:false} twin) -- at /usr/lib/systemd/user/<pkg>.service.
// Nothing auto-enables it (no maintainer scripts); the documented enablement
// is `systemctl --user enable --now <pkg>`.
func TestDebRepackage_ServiceUnit(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	input.Project.CreateService = true

	output, err := rp.Repackage(context.Background(), input)
	require.NoError(t, err)
	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())

	entries := tarGzEntries(t, readArMembers(t, data)["data.tar.gz"])
	unit, ok := entries["./usr/lib/systemd/user/testapp.service"]
	require.True(t, ok, "expected service unit, got entries: %v", keysOf(entries))

	want := "[Unit]\n" +
		"Description=A test application\n" +
		"After=graphical-session.target\n" +
		"PartOf=graphical-session.target\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=/usr/bin/testapp\n" +
		"Restart=on-failure\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=graphical-session.target\n"
	assert.Equal(t, want, unit)

	_, ok = entries["./usr/bin/testapp"]
	assert.True(t, ok, "binary entry must still be present")
}

// The auto-setup half: a flagged deb carries maintainer scripts that enable
// the unit at install (postinst: `systemctl --global enable`, guaranteed
// active at each user's next graphical login, plus a strictly best-effort
// immediate start for the sudo-invoking user's live session) and disable it
// on remove (prerm). Every action is guarded so a systemctl-less host never
// leaves the package unconfigured.
func TestDebRepackage_ServiceMaintainerScripts(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	input.Project.CreateService = true

	output, err := rp.Repackage(context.Background(), input)
	require.NoError(t, err)
	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())

	control := tarGzEntries(t, readArMembers(t, data)["control.tar.gz"])
	postinst, ok := control["./postinst"]
	require.True(t, ok, "expected postinst, got: %v", keysOf(control))
	assert.True(t, strings.HasPrefix(postinst, "#!/bin/sh\n"))
	assert.Contains(t, postinst, "systemctl --global enable testapp.service")
	assert.Contains(t, postinst, `systemctl --user -M "${SUDO_USER}@" start testapp.service`)

	prerm, ok := control["./prerm"]
	require.True(t, ok, "expected prerm, got: %v", keysOf(control))
	assert.Contains(t, prerm, "systemctl --global disable testapp.service")
}

// Flag off: the data member carries EXACTLY the one binary entry and the
// control member EXACTLY the control file -- the whole content surface of the
// pre-setting deb, so off-state debs stay identical.
func TestDebRepackage_ServiceOffNoUnit(t *testing.T) {
	rp := &Deb{}
	output, err := rp.Repackage(context.Background(), makeInput())
	require.NoError(t, err)
	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())

	members := readArMembers(t, data)
	entries := tarGzEntries(t, members["data.tar.gz"])
	require.Len(t, entries, 1, "flag-off data.tar must hold only the binary, got: %v", keysOf(entries))
	_, ok := entries["./usr/bin/testapp"]
	assert.True(t, ok)

	control := tarGzEntries(t, members["control.tar.gz"])
	require.Len(t, control, 1, "flag-off control.tar must hold only ./control, got: %v", keysOf(control))
	_, ok = control["./control"]
	assert.True(t, ok)
}

// Non-binary kinds never ship the unit: its ExecStart is the /usr/bin install
// path, which only the binary kind stages.
func TestDebRepackage_ServiceNonBinaryKindNoUnit(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	input.Project.CreateService = true
	input.Artifact.Kind = db.KindArchive

	output, err := rp.Repackage(context.Background(), input)
	require.NoError(t, err)
	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())

	for name := range tarGzEntries(t, readArMembers(t, data)["data.tar.gz"]) {
		assert.NotContains(t, name, ".service")
	}
}

// Slash-namespaced projects fold to the deb package name everywhere the unit
// references itself: file name and ExecStart both use the folded name.
func TestDebRepackage_ServiceUnitNamespacedName(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	input.Project.Name = "myrepo/myapp"
	input.Project.CreateService = true

	output, err := rp.Repackage(context.Background(), input)
	require.NoError(t, err)
	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())

	entries := tarGzEntries(t, readArMembers(t, data)["data.tar.gz"])
	unit, ok := entries["./usr/lib/systemd/user/myrepo-myapp.service"]
	require.True(t, ok, "expected folded-name unit, got: %v", keysOf(entries))
	assert.Contains(t, unit, "ExecStart=/usr/bin/myrepo-myapp\n")
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
