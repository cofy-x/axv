#!/usr/bin/env sh
# AXV single-binary installer for macOS and Linux.
# Downloads the official GitHub Release archive, verifies SHA-256 checksums,
# and installs the `axv` executable atomically.
set -eu

REPO="${AXV_RELEASE_REPO:-cofy-x/axv}"
API_BASE="${AXV_RELEASE_API_BASE:-https://api.github.com/repos/${REPO}}"
DOWNLOAD_BASE="${AXV_RELEASE_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download}"
VERSION=""
BIN_DIR=""

usage() {
  cat <<'EOF'
Install the AXV single-binary workstation CLI and embedded desktop.

Usage:
  install.sh [--version <version>] [--bin-dir <dir>]

Options:
  --version <ver>   Release version without leading 'v' (default: latest matching release or prerelease)
  --bin-dir <dir>   Destination directory (default: /usr/local/bin if root, else ~/.local/bin)
  -h, --help        Show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      [ $# -ge 2 ] || { echo "error: --version requires a value" >&2; exit 2; }
      VERSION="${2#v}"
      shift 2
      ;;
    --bin-dir)
      [ $# -ge 2 ] || { echo "error: --bin-dir requires a value" >&2; exit 2; }
      BIN_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command '$1' was not found on PATH" >&2
    exit 1
  fi
}

need_cmd curl
need_cmd tar
need_cmd awk

OS_RAW="$(uname -s)"
ARCH_RAW="$(uname -m)"

case "$OS_RAW" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)
    echo "error: unsupported operating system: $OS_RAW (AXV supports Linux and macOS)" >&2
    exit 1
    ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: $ARCH_RAW (supported: amd64, arm64)" >&2
    exit 1
    ;;
esac

if [ -z "$BIN_DIR" ]; then
  if [ "$(id -u)" -eq 0 ]; then
    BIN_DIR="/usr/local/bin"
  else
    BIN_DIR="${HOME}/.local/bin"
  fi
fi

fetch() {
  if [ "${AXV_TEST_ALLOW_HTTP:-0}" = "1" ]; then
    curl --fail --silent --show-error --location "$@"
  else
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$@"
  fi
}

# Extract the first non-draft AXV application release version (excluding Guest
# releases) whose assets include axv_<ver>_<os>_<arch>.tar.gz.
# Uses POSIX awk only so it works identically on macOS BSD awk and GNU awk.
extract_matching_version() {
  awk -v os="$OS" -v arch="$ARCH" '
  { buf = buf " " $0 }
  END {
    n = split(buf, recs, /"tag_name"/)
    for (i = 2; i <= n; i++) {
      rec = recs[i]
      if (rec ~ /"draft"[[:space:]]*:[[:space:]]*true/) continue
      colon = index(rec, ":")
      if (colon == 0) continue
      rest = substr(rec, colon + 1)
      q1 = index(rest, "\"")
      if (q1 == 0) continue
      rest = substr(rest, q1 + 1)
      q2 = index(rest, "\"")
      if (q2 == 0) continue
      tag = substr(rest, 1, q2 - 1)
      if (substr(tag, 1, 1) != "v") continue
      ver = substr(tag, 2)
      if (ver !~ /^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$/) continue
      asset = "\"axv_" ver "_" os "_" arch ".tar.gz\""
      if (index(rec, asset) > 0) {
        print ver
        exit
      }
    }
  }'
}

if [ -z "$VERSION" ]; then
  echo "Resolving latest AXV release for ${OS}/${ARCH} from https://github.com/${REPO}..."
  LATEST_JSON="$(fetch "${API_BASE}/releases/latest" 2>/dev/null || true)"
  if [ -n "$LATEST_JSON" ]; then
    VERSION="$(printf '%s\n' "$LATEST_JSON" | extract_matching_version)"
  fi
  if [ -z "$VERSION" ]; then
    RELEASES_JSON="$(fetch "${API_BASE}/releases?per_page=30")"
    VERSION="$(printf '%s\n' "$RELEASES_JSON" | extract_matching_version)"
  fi
  if [ -z "$VERSION" ]; then
    echo "error: no published AXV application release found for ${OS}/${ARCH}; specify --version <version>" >&2
    exit 1
  fi
fi

TAG="v${VERSION}"
BUNDLE="axv_${VERSION}_${OS}_${ARCH}"
ARCHIVE="${BUNDLE}.tar.gz"
BASE_URL="${DOWNLOAD_BASE}/${TAG}"

TMPDIR_WORK="$(mktemp -d "${TMPDIR:-/tmp}/axv-install.XXXXXX")"
cleanup() {
  rm -rf "$TMPDIR_WORK"
}
trap cleanup EXIT INT TERM

echo "Downloading ${ARCHIVE} (${TAG})..."
fetch --output "${TMPDIR_WORK}/${ARCHIVE}" "${BASE_URL}/${ARCHIVE}"
fetch --output "${TMPDIR_WORK}/SHA256SUMS" "${BASE_URL}/SHA256SUMS"

echo "Verifying SHA-256 checksum..."
EXPECTED="$(awk -v file="$ARCHIVE" '$2 == file || $2 == ("*" file) { print $1; exit }' "${TMPDIR_WORK}/SHA256SUMS")"
if [ -z "$EXPECTED" ]; then
  echo "error: ${ARCHIVE} is not listed in SHA256SUMS" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMPDIR_WORK}/${ARCHIVE}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMPDIR_WORK}/${ARCHIVE}" | awk '{print $1}')"
else
  echo "error: sha256sum or shasum is required for checksum verification" >&2
  exit 1
fi

if [ "$ACTUAL" != "$EXPECTED" ]; then
  echo "error: checksum mismatch for ${ARCHIVE}" >&2
  echo "  expected: ${EXPECTED}" >&2
  echo "  actual:   ${ACTUAL}" >&2
  exit 1
fi

tar -xzf "${TMPDIR_WORK}/${ARCHIVE}" -C "${TMPDIR_WORK}"
SRC_BIN="${TMPDIR_WORK}/${BUNDLE}/axv"
if [ ! -f "$SRC_BIN" ]; then
  echo "error: archive did not contain ${BUNDLE}/axv" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"
TMP_DEST="${BIN_DIR}/.axv.install.$$"
cp "$SRC_BIN" "$TMP_DEST"
chmod 0755 "$TMP_DEST"
mv -f "$TMP_DEST" "${BIN_DIR}/axv"

INSTALLED_VER="$("${BIN_DIR}/axv" version)"
echo "Installed ${INSTALLED_VER} to ${BIN_DIR}/axv"

case ":${PATH}:" in
  *":${BIN_DIR}:"*) ;;
  *)
    echo ""
    echo "Add ${BIN_DIR} to your PATH:"
    echo "  export PATH=\"${BIN_DIR}:\$PATH\""
    ;;
esac

echo ""
echo "Next steps:"
echo "  axv doctor                     # check local environment and prerequisites"
echo "  axv                            # start the workstation (or: axv daemon start)"
if [ "$OS" = "linux" ]; then
  echo "  sudo axv host install-service --apply-sysctl   # install Linux Firecracker VM service"
  echo "  axv remote enable              # enable Tailscale HTTPS remote access"
else
  echo "  axv setup                      # prepare macOS Lima VM hosting"
fi
