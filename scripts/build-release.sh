#!/usr/bin/env bash
# Cross-compile devproxy-edge and devproxy-agent for all release platforms into
# per-platform tarballs plus a checksums file.
#
# Usage: scripts/build-release.sh [VERSION]
#   VERSION defaults to $VERSION env, or "dev".
#   Output dir defaults to $DIST env, or "dist".
set -euo pipefail

VERSION="${1:-${VERSION:-dev}}"
DIST="${DIST:-dist}"
MODULE="github.com/alertd/devproxy"
PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

LDFLAGS="-s -w -X ${MODULE}/internal/buildinfo.Version=${VERSION}"

rm -rf "$DIST"
mkdir -p "$DIST"

for platform in $PLATFORMS; do
	os="${platform%/*}"
	arch="${platform#*/}"
	echo ">> building ${os}/${arch}"

	workdir="$(mktemp -d)"
	for cmd in edge agent; do
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
			go build -trimpath -ldflags "$LDFLAGS" \
			-o "$workdir/devproxy-${cmd}" "./cmd/${cmd}"
	done

	tar -C "$workdir" -czf "$DIST/devproxy_${os}_${arch}.tar.gz" \
		devproxy-edge devproxy-agent
	rm -rf "$workdir"
done

# Checksums (sha256sum on Linux, shasum on macOS).
echo ">> writing checksums"
(
	cd "$DIST"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum ./*.tar.gz > checksums.txt
	else
		shasum -a 256 ./*.tar.gz > checksums.txt
	fi
)

echo ">> done; artifacts in $DIST/"
ls -1 "$DIST"
