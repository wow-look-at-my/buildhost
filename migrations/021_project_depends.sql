-- The buildhost projects this project's packages depend on, as canonical
-- project names joined by commas. The deb Depends field, the APT install.sh
-- and the brew depends_on lines all come from it. A rename of a dependency
-- rewrites every list that names it.
ALTER TABLE projects ADD COLUMN depends TEXT NOT NULL DEFAULT '';
