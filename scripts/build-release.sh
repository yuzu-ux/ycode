#!/bin/sh

set -eu

root_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
version="${1:-${VERSION:-}}"
go_bin="${GO:-go}"

if [ -z "$version" ]; then
	printf 'usage: %s VERSION\n' "$0" >&2
	exit 2
fi

case "$version" in
	v*) binary_version="${version#v}" ;;
	*) binary_version="$version" ;;
esac

case "$binary_version" in
	"" | *[!0-9A-Za-z._+-]*)
		printf 'invalid version: %s\n' "$version" >&2
		exit 2
		;;
esac

command -v "$go_bin" >/dev/null 2>&1 || {
	printf '%s is required\n' "$go_bin" >&2
	exit 1
}
command -v tar >/dev/null 2>&1 || {
	printf 'tar is required\n' >&2
	exit 1
}
command -v zip >/dev/null 2>&1 || {
	printf 'zip is required\n' >&2
	exit 1
}

dist_dir="$root_dir/dist"
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ycode-release.XXXXXX")"
cleanup() {
	case "$build_dir" in
		*/ycode-release.*) rm -rf -- "$build_dir" ;;
	esac
}
trap cleanup EXIT HUP INT TERM

rm -rf -- "$dist_dir"
mkdir -p "$dist_dir"

for target in \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64
do
	os="${target%/*}"
	arch="${target#*/}"
	package="ycode-${os}-${arch}"
	binary="$build_dir/ycode"

	if [ "$os" = "windows" ]; then
		binary="$build_dir/ycode.exe"
	fi

	printf 'Building %s/%s...\n' "$os" "$arch"
	(
		cd "$root_dir"
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$go_bin" build \
			-trimpath \
			-ldflags "-s -w -X main.version=$binary_version" \
			-o "$binary" \
			./cmd/ycode
	)

	if [ "$os" = "windows" ]; then
		(
			cd "$build_dir"
			zip -q "$dist_dir/$package.zip" ycode.exe
		)
		rm -f -- "$binary"
	else
		tar -C "$build_dir" -czf "$dist_dir/$package.tar.gz" ycode
		rm -f -- "$binary"
	fi
done

(
	cd "$dist_dir"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum *.tar.gz *.zip >SHA256SUMS
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 *.tar.gz *.zip >SHA256SUMS
	else
		printf 'sha256sum or shasum is required\n' >&2
		exit 1
	fi
)

printf 'Release packages written to %s\n' "$dist_dir"
