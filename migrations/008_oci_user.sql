-- Run-as user for OCI images synthesized from a binary, chosen by the publisher at
-- release-create time. Format: uid[:gid] or username[:groupname]. Empty (the default)
-- means the image default user (root). Applies only to the synthesized-binary OCI path;
-- pushed docker images are unaffected.
ALTER TABLE releases ADD COLUMN oci_user TEXT NOT NULL DEFAULT '';
