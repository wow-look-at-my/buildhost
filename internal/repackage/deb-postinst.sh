#!/bin/sh
set -e
if [ "$1" = "configure" ] && command -v systemctl >/dev/null 2>&1; then
    systemctl --global enable %s.service >/dev/null 2>&1 || true
    if [ -n "${SUDO_USER:-}" ] && [ "${SUDO_USER}" != "root" ]; then
        systemctl --user -M "${SUDO_USER}@" start %s.service >/dev/null 2>&1 || true
    fi
fi
