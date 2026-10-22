#!/bin/sh
# buildhost APT repository installer Project.
set -eu

PROJECT='__PROJECT__'
PKG='__PKG__'
APT_URL='__APT_URL__'
PRIVATE_URL='__PRIVATE_URL__'
KEYRING='/etc/apt/keyrings/__SLUG__.asc'
LIST='/etc/apt/sources.list.d/__SLUG__.list'
PRIVATE_KEYRING='/etc/apt/keyrings/buildhost-private.asc'
PRIVATE_LIST='/etc/apt/sources.list.d/buildhost-private.list'
PRIVATE_AUTH='/etc/apt/auth.conf.d/buildhost-private.conf'
TOKEN="${BUILDHOST_TOKEN:-}"

if [ "$(id -u)" -ne 0 ]; then
    echo "buildhost: this installer must run as root (pipe it to 'sudo sh')." >&2
    exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
    echo "buildhost: curl is required but was not found on PATH." >&2
    exit 1
fi

mkdir -p /etc/apt/keyrings

# Each buildhost project this package depends on is its own APT repository.
# add_dep adds one the same way as this project's, so apt-get can resolve the Depends.
add_dep() {
    dep_name="$1"
    dep_url="$2"
    dep_slug="$3"
    dep_private="$4"
    if [ "$dep_private" = 1 ]; then
        echo "buildhost: dependency '$dep_name' is a private project; pass a read token in BUILDHOST_TOKEN." >&2
        exit 1
    fi
    curl -fsSL "$dep_url/key.asc" -o "/etc/apt/keyrings/$dep_slug.asc"
    chmod 0644 "/etc/apt/keyrings/$dep_slug.asc"
    echo "deb [signed-by=/etc/apt/keyrings/$dep_slug.asc] $dep_url stable main" > "/etc/apt/sources.list.d/$dep_slug.list"
}

if [ -n "$TOKEN" ]; then
    # With a token, one authenticated source lists this project.
    curl -fsSL -H "Authorization: Bearer $TOKEN" "$PRIVATE_URL/key.asc" -o "$PRIVATE_KEYRING"
    chmod 0644 "$PRIVATE_KEYRING"
    echo "deb [signed-by=$PRIVATE_KEYRING] $PRIVATE_URL stable main" > "$PRIVATE_LIST"
    mkdir -p /etc/apt/auth.conf.d
    {
        echo "machine $PRIVATE_URL"
        echo "login token"
        echo "password $TOKEN"
    } > "$PRIVATE_AUTH"
    chmod 0600 "$PRIVATE_AUTH"
else
    curl -fsSL "$APT_URL/key.asc" -o "$KEYRING"
    chmod 0644 "$KEYRING"
    echo "deb [signed-by=$KEYRING] $APT_URL stable main" > "$LIST"
__DEPENDS__
fi

apt-get update

echo ""
echo "buildhost: repository for '$PROJECT' added."
echo "Install it with:  sudo apt-get install $PKG"
