-- The project's runtime prerequisites, in Debian relationship syntax (e.g.
-- "bubblewrap | docker.io"). The generated deb and the APT Packages entry
-- carry it as a Depends field. The publishing repo's CI declares it on
-- release-create, or an operator sets it through PATCH /api/v1/projects/{name}.
-- The empty default emits no Depends line.
ALTER TABLE projects ADD COLUMN apt_depends TEXT NOT NULL DEFAULT '';
