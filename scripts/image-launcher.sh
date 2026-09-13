#!/bin/sh
# The image installs this at /usr/local/bin/buildhost, and the binary beside it
# under /usr/local/lib. A rolling updater creates the replacement container from
# the config of the container it replaces, so the entrypoint that reaches this
# image is whatever the OLD one recorded. A shebang script is execable, so every
# spelling still reaches the binary -- the image's own, a "buildhost" found on
# PATH, and an absolute path.
#
# The path below holds a real ELF: the image stages the APE at build time, with
# the header its own trampoline would have written. Nothing unpacks at run time.
#
# The trampoline used to run here instead, and it stages its copy under a
# hardcoded /tmp/.ape-run-1-$(id -u) that it picks itself. It reads no TMPDIR, so
# a deployment's noexec /tmp killed every container at exit 126, against a path
# nobody chose. Searching for a writable, executable directory and exporting
# TMPDIR could not fix that, because nothing ever read the variable.
real=/usr/local/lib/buildhost/buildhost

exec "$real" "$@"
