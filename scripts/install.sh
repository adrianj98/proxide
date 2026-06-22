#!/usr/bin/env sh
# devproxy installer. Downloads the release tarball for the current OS/arch and
# installs devproxy-edge and devproxy-agent into a bin directory.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/alertd/devproxy/main/scripts/install.sh | sh
#
# Overrides (env vars):
#   DEVPROXY_VERSION  release tag to install (default: latest)
#   DEVPROXY_BIN_DIR  install directory (default: /usr/local/bin)
#   DEVPROXY_REPO     owner/repo (default: alertd/devproxy)
set -eu

REPO="${DEVPROXY_REPO:-alertd/devproxy}"
VERSION="${DEVPROXY_VERSION:-latest}"
BIN_DIR="${DEVPROXY_BIN_DIR:-/usr/local/bin}"

err() {
	echo "install: $1" >&2
	exit 1
}

# Detect OS.
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
	linux) os="linux" ;;
	darwin) os="darwin" ;;
	*) err "unsupported OS: $os (linux and darwin only)" ;;
esac

# Detect architecture.
arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) err "unsupported architecture: $arch (amd64 and arm64 only)" ;;
esac

asset="devproxy_${os}_${arch}.tar.gz"
if [ "$VERSION" = "latest" ]; then
	url="https://github.com/${REPO}/releases/latest/download/${asset}"
else
	url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
fi

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "install: downloading $url"
curl -fsSL "$url" -o "$tmp/$asset" || err "download failed (check version/arch/repo)"
tar -C "$tmp" -xzf "$tmp/$asset" || err "extract failed"

# Install both binaries, using sudo only when the target dir is not writable.
SUDO=""
if [ ! -w "$BIN_DIR" ]; then
	if command -v sudo >/dev/null 2>&1; then
		SUDO="sudo"
		echo "install: $BIN_DIR is not writable; using sudo"
	else
		err "$BIN_DIR is not writable and sudo is unavailable; set DEVPROXY_BIN_DIR"
	fi
fi

$SUDO mkdir -p "$BIN_DIR"
for name in devproxy-edge devproxy-agent; do
	chmod +x "$tmp/$name"
	$SUDO install -m 0755 "$tmp/$name" "$BIN_DIR/$name"
	echo "install: installed $BIN_DIR/$name"
done

echo "install: done. Run 'devproxy-edge -version' / 'devproxy-agent -version' to verify."
