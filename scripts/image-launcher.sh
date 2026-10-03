#!/bin/sh
# The image installs this at /usr/local/bin/buildhost, and the binary beside it under /usr/local/lib.
real=/usr/local/lib/buildhost/buildhost

exec "$real" "$@"
