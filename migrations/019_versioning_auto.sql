-- A semver project refuses a versionless publish, and every publisher now sends none.
UPDATE projects SET versioning = 'auto' WHERE versioning = 'semver';
