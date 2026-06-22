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
PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

LDFLAGS="-s -w -X ${MODULE}/internal/buildinfo.Version=${VERSION}"

rm -rf "$DIST"
mkdir -p "$DIST"
DIST="$(cd "$DIST" && pwd)" # absolute, so zip/tar land correctly

for platform in $PLATFORMS; do
	os="${platform%/*}"
	arch="${platform#*/}"
	echo ">> building ${os}/${arch}"

	ext=""
	[ "$os" = "windows" ] && ext=".exe"

	workdir="$(mktemp -d)"
	for cmd in edge agent; do
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
			go build -trimpath -ldflags "$LDFLAGS" \
			-o "$workdir/devproxy-${cmd}${ext}" "./cmd/${cmd}"
	done

	# Windows users expect .zip; everyone else gets .tar.gz.
	if [ "$os" = "windows" ]; then
		( cd "$workdir" && zip -q "$DIST/devproxy_${os}_${arch}.zip" \
			"devproxy-edge${ext}" "devproxy-agent${ext}" )
	else
		tar -C "$workdir" -czf "$DIST/devproxy_${os}_${arch}.tar.gz" \
			"devproxy-edge${ext}" "devproxy-agent${ext}"
	fi
	rm -rf "$workdir"
done

# Checksums (sha256sum on Linux, shasum on macOS).
echo ">> writing checksums"
(
	cd "$DIST"
	shopt -s nullglob
	archives=(*.tar.gz *.zip)
	shopt -u nullglob
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "${archives[@]}" > checksums.txt
	else
		shasum -a 256 "${archives[@]}" > checksums.txt
	fi
)

echo ">> done; artifacts in $DIST/"
ls -1 "$DIST"
