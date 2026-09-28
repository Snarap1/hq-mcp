#!/bin/sh
# Install hq-mcp from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh -s -- --version v0.1.0 --dir /usr/local/bin
#
# Environment overrides:
#   HQ_MCP_VERSION         release tag to install (default: the latest release)
#   HQ_MCP_INSTALL_DIR     install directory (default: $HOME/.local/bin)
#   HQ_MCP_DOWNLOAD_BASE   release asset base URL (default: the GitHub releases download URL; used by tests)
set -eu

REPO="${HQ_MCP_REPO:-Snarap1/hq-mcp}"
BINARY="hq-mcp"
TAG="${HQ_MCP_VERSION:-}"
DIR="${HQ_MCP_INSTALL_DIR:-$HOME/.local/bin}"

usage() {
	cat <<'EOF'
usage: install.sh [--version <tag>] [--dir <path>]

Install hq-mcp from a GitHub release:

  curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh -s -- --version v0.1.0 --dir /usr/local/bin

Environment overrides:
  HQ_MCP_VERSION         release tag to install (default: the latest release)
  HQ_MCP_INSTALL_DIR     install directory (default: $HOME/.local/bin)
  HQ_MCP_DOWNLOAD_BASE   release asset base URL (default: the GitHub releases download URL; used by tests)
  HQ_MCP_REPO            owner/name of the GitHub repo to install from (default: Snarap1/hq-mcp)
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	-v | --version)
		TAG="$2"
		shift 2
		;;
	-d | --dir)
		DIR="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'install.sh: unknown argument: %s\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

die() {
	printf 'hq-mcp install: %s\n' "$1" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
FreeBSD) os=freebsd ;;
MINGW* | MSYS* | CYGWIN*) os=windows ;;
*) die "unsupported OS: $(uname -s); build from source instead: go install github.com/$REPO@latest" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7l | armv7) arch=armv7 ;;
*) die "unsupported architecture: $(uname -m); build from source instead: go install github.com/$REPO@latest" ;;
esac

fetch() {
	url="$1"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$url"
	else
		die "neither curl nor wget is available"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/.*= *//'
	else
		die "no sha256 tool (sha256sum, shasum, openssl) found; cannot verify the download"
	fi
}

# Asset names carry no version, so an untagged install can use the
# releases/latest/download alias and never has to resolve the latest tag.
if [ -n "${HQ_MCP_DOWNLOAD_BASE:-}" ]; then
	base="${HQ_MCP_DOWNLOAD_BASE%/}"
elif [ -n "$TAG" ]; then
	base="https://github.com/$REPO/releases/download/$TAG"
else
	base="https://github.com/$REPO/releases/latest/download"
fi

if [ "$os" = "windows" ]; then
	archive="$BINARY.exe"
	ext="zip"
else
	archive="$BINARY"
	ext="tar.gz"
fi
asset="${BINARY}_${os}_${arch}.${ext}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

printf 'hq-mcp install: downloading from %s (%s/%s)\n' "$base" "$os" "$arch"
fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"

want=$(sha256_of "$tmp/$asset")
have=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt" | head -1)
[ -n "$have" ] || die "no checksum for $asset in checksums.txt"
[ "$want" = "$have" ] || die "checksum mismatch for $asset: got $have, downloaded $want"

if [ "$ext" = "zip" ]; then
	# Windows Git-Bash ships bsdtar as tar.exe and usually unzip; GNU tar cannot read zip.
	if command -v bsdtar >/dev/null 2>&1; then
		bsdtar -xf "$tmp/$asset" -C "$tmp" "$archive" || die "cannot unpack $asset"
	elif command -v unzip >/dev/null 2>&1; then
		unzip -qo "$tmp/$asset" "$archive" -d "$tmp" || die "cannot unpack $asset"
	else
		die "bsdtar or unzip is needed to unpack $asset"
	fi
else
	tar -xzf "$tmp/$asset" -C "$tmp" "$archive" || die "cannot unpack $asset"
fi
chmod +x "$tmp/$archive"

mkdir -p "$DIR"
target="$DIR/$BINARY"
[ "$os" = "windows" ] && target="$target.exe"
cp "$tmp/$archive" "$target.tmp.$$"
chmod +x "$target.tmp.$$"
mv -f "$target.tmp.$$" "$target"

printf 'hq-mcp install: installed %s\n' "$target"

case ":${PATH:-}:" in
*":$DIR:"*) ;;
*) printf 'hq-mcp install: %s is not on PATH; add it to your shell profile\n' "$DIR" ;;
esac

printf 'hq-mcp install: next: register the server, e.g. claude mcp add hq-mcp -- %s\n' "$target"
