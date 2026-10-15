#!/bin/sh
# Copyright 2026 Brynko Technologies
# SPDX-License-Identifier: AGPL-3.0-only
# Installs brynko-pulse on Linux from the GitHub releases.
#
#   curl -fsSL https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh | sudo sh
#
# Set PULSE_VERSION=v1.2.3 to install a specific release. The download is
# checked against the release's SHA256SUMS before anything is installed.
set -eu

REPO="Aadilkaifi/brynko-pulse"
VERSION="${PULSE_VERSION:-}"

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

[ "$(uname -s)" = Linux ] || fail "this script is for Linux. For Windows see https://github.com/$REPO/blob/main/docs/install.md"
[ "$(id -u)" = 0 ] || fail "run it as root, for example: curl -fsSL <url> | sudo sh"

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) fail "this CPU ($(uname -m)) is not supported yet" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
	latest() { curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
	latest() { wget -q -S --spider "https://github.com/$REPO/releases/latest" 2>&1 | sed -n 's/^ *Location: *//p' | tail -1; }
else
	fail "curl or wget is needed"
fi

if [ -z "$VERSION" ]; then
	# The latest-release page redirects to .../releases/tag/<version>.
	VERSION=$(latest | tr -d '\r' | sed 's#.*/tag/##')
fi
case "$VERSION" in
v[0-9]*) ;;
*) fail "could not find the latest release. Set PULSE_VERSION, for example PULSE_VERSION=v0.1.0" ;;
esac

if command -v apt-get >/dev/null 2>&1 && command -v dpkg >/dev/null 2>&1; then
	KIND=deb FILE="brynko-pulse_${VERSION}_linux_${ARCH}.deb"
elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
	KIND=rpm FILE="brynko-pulse_${VERSION}_linux_${ARCH}.rpm"
else
	KIND=bin FILE="pulse_${VERSION}_linux_${ARCH}"
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
BASE="https://github.com/$REPO/releases/download/$VERSION"

echo "Downloading $FILE"
fetch "$BASE/$FILE" "$TMP/$FILE" || fail "could not download $BASE/$FILE"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || fail "could not download SHA256SUMS"

want=$(awk -v f="$FILE" '$2 == f || $2 == "*" f { print $1 }' "$TMP/SHA256SUMS")
[ -n "$want" ] || fail "$FILE is not listed in SHA256SUMS. Nothing was installed."
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$TMP/$FILE" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$TMP/$FILE" | awk '{ print $1 }')
fi
[ "$want" = "$got" ] || fail "the checksum of $FILE does not match: the download is damaged or was changed. Nothing was installed."
echo "Checksum verified"

case $KIND in
deb)
	DEBIAN_FRONTEND=noninteractive apt-get install -y "$TMP/$FILE"
	;;
rpm)
	if command -v dnf >/dev/null 2>&1; then dnf install -y "$TMP/$FILE"; else yum install -y "$TMP/$FILE"; fi
	;;
bin)
	# Installed straight into place: /tmp is often mounted without exec rights.
	install -m 0755 "$TMP/$FILE" /usr/local/bin/pulse
	/usr/local/bin/pulse install
	;;
esac

echo
echo "brynko-pulse $VERSION is installed. Next: add where alerts go, see"
echo "https://github.com/$REPO/blob/main/docs/channels.md"
