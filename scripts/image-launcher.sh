#!/bin/sh
# The image installs this at /usr/local/bin/buildhost, and the APE beside it
# under /usr/local/lib. The kernel cannot exec an APE: the file's header is a
# shell script, and the image registers no binfmt handler. A shebang script IS
# execable, so every spelling of the entrypoint reaches the binary -- the
# image's own, a "buildhost" found on PATH, and an absolute path.
exec /bin/sh /usr/local/lib/buildhost/buildhost "$@"
