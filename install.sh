#!/bin/sh

set -eu

repo="yuzu-ux/ycode"
version="${YCODE_VERSION:-latest}"

fail() {
	printf 'ycode installer: %s\n' "$*" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"

case "$(uname -s)" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
esac

asset="ycode-${os}-${arch}.tar.gz"

if [ -n "${YCODE_RELEASE_BASE_URL:-}" ]; then
	download_base="${YCODE_RELEASE_BASE_URL%/}"
elif [ "$version" = "latest" ]; then
	download_base="https://github.com/${repo}/releases/latest/download"
else
	case "$version" in
		v*) tag="$version" ;;
		*) tag="v$version" ;;
	esac
	download_base="https://github.com/${repo}/releases/download/${tag}"
fi

if [ -n "${YCODE_INSTALL_DIR:-}" ]; then
	install_dir="$YCODE_INSTALL_DIR"
elif [ -n "${HOME:-}" ]; then
	install_dir="$HOME/.local/bin"
else
	fail "HOME is not set; provide YCODE_INSTALL_DIR"
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ycode.XXXXXX")"
cleanup() {
	case "$tmp_dir" in
		*/ycode.*) rm -rf -- "$tmp_dir" ;;
	esac
}
trap cleanup EXIT HUP INT TERM

archive="$tmp_dir/$asset"
checksums="$tmp_dir/SHA256SUMS"

printf 'Downloading %s...\n' "$asset"
curl -fsSL --retry 3 "$download_base/$asset" -o "$archive"
curl -fsSL --retry 3 "$download_base/SHA256SUMS" -o "$checksums"

expected="$(
	awk -v asset="$asset" '
		$2 == asset || $2 == "*" asset {
			print tolower($1)
			exit
		}
	' "$checksums"
)"
[ -n "$expected" ] || fail "no checksum found for $asset"

if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$archive" | awk '{ print tolower($1) }')"
elif command -v shasum >/dev/null 2>&1; then
	actual="$(shasum -a 256 "$archive" | awk '{ print tolower($1) }')"
else
	fail "sha256sum or shasum is required to verify the download"
fi

[ "$actual" = "$expected" ] || fail "checksum verification failed for $asset"
printf 'Checksum verified.\n'

(cd "$tmp_dir" && tar -xzf "$asset")
[ -f "$tmp_dir/ycode" ] || fail "release archive does not contain ycode"

mkdir -p "$install_dir"
pending="$install_dir/.ycode-install.$$"
cp "$tmp_dir/ycode" "$pending"
chmod 0755 "$pending"
mv -f "$pending" "$install_dir/ycode"

printf 'Installed YCode to %s\n' "$install_dir/ycode"
"$install_dir/ycode" version

case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*)
		printf '\nAdd YCode to your PATH:\n'
		printf '  export PATH="%s:$PATH"\n' "$install_dir"
		;;
esac
