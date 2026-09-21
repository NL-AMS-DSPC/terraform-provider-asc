#!/usr/bin/env bash
set -euo pipefail

GORELEASER_VERSION="v2.18.2"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

EXPECTED_PLATFORMS=(
  darwin_amd64
  darwin_arm64
  linux_amd64
  linux_arm64
  windows_amd64
  windows_arm64
)

fail() {
  echo "release-snapshot-check: FAIL: $*" >&2
  exit 1
}

TMP_GNUPGHOME=""
TOOL_DIR=""
cleanup() {
  if [ -n "$TMP_GNUPGHOME" ] && [ -d "$TMP_GNUPGHOME" ]; then
    GNUPGHOME="$TMP_GNUPGHOME" gpgconf --kill all >/dev/null 2>&1 || true
    rm -rf "$TMP_GNUPGHOME"
  fi
  if [ -n "$TOOL_DIR" ]; then
    rm -rf "$TOOL_DIR"
  fi
}
trap cleanup EXIT

for tool in go gpg unzip python3; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool not on PATH: $tool"
done

if command -v sha256sum >/dev/null 2>&1; then
  sha256_check() { sha256sum -c "$1"; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_check() { shasum -a 256 -c "$1"; }
else
  fail "no sha256sum or shasum on PATH"
fi

TMP_GNUPGHOME="$(mktemp -d "${TMPDIR:-/tmp}/asc-release-gnupg.XXXXXX")"
chmod 700 "$TMP_GNUPGHOME"
export GNUPGHOME="$TMP_GNUPGHOME"

echo "==> generating throwaway signing key in $GNUPGHOME"
gpg --batch --quiet --gen-key <<'EOF'
%no-protection
Key-Type: RSA
Key-Length: 2048
Key-Usage: sign
Name-Real: ASC Release Snapshot Check
Name-Email: release-snapshot-check@invalid
Expire-Date: 0
%commit
EOF

GPG_FINGERPRINT="$(gpg --batch --with-colons --list-secret-keys --fingerprint |
  awk -F: '/^fpr:/ { print $10; exit }')"
[ -n "$GPG_FINGERPRINT" ] || fail "could not read the throwaway key fingerprint"
export GPG_FINGERPRINT

echo "==> running goreleaser $GORELEASER_VERSION snapshot release"
# Install separately so GoReleaser cannot change the provider build toolchain.
TOOL_DIR="$(mktemp -d "${TMPDIR:-/tmp}/asc-release-tools.XXXXXX")"
GOBIN="$TOOL_DIR" go install "github.com/goreleaser/goreleaser/v2@${GORELEASER_VERSION}"
"$TOOL_DIR/goreleaser" release --snapshot --clean

[ -f dist/metadata.json ] || fail "goreleaser produced no dist/metadata.json"
VERSION="$(python3 -c 'import json,sys; print(json.load(open("dist/metadata.json"))["version"])')"
[ -n "$VERSION" ] || fail "dist/metadata.json carries no version"
echo "==> asserting artifacts for snapshot version $VERSION"

for platform in "${EXPECTED_PLATFORMS[@]}"; do
  archive="dist/terraform-provider-asc_${VERSION}_${platform}.zip"
  [ -f "$archive" ] || fail "missing archive $archive"
  expected_binary="terraform-provider-asc_v${VERSION}"
  case "$platform" in windows_*) expected_binary="${expected_binary}.exe" ;; esac
  archive_contents="$(unzip -Z1 "$archive")"
  [ "$archive_contents" = "$expected_binary" ] ||
    fail "$platform archive holds '$archive_contents', expected exactly '$expected_binary'"
done

MANIFEST="dist/terraform-provider-asc_${VERSION}_manifest.json"
[ -f "$MANIFEST" ] || fail "missing registry manifest $MANIFEST"
python3 - "$MANIFEST" <<'EOF'
import json, sys

path = sys.argv[1]
with open(path) as handle:
    doc = json.load(handle)
versions = doc.get("metadata", {}).get("protocol_versions")
if doc.get("version") != 1 or versions != ["6.0"]:
    sys.exit(f"{path} must declare manifest version 1 and protocol_versions ['6.0']")
print(f"    manifest protocol_versions: {versions}")
EOF
cmp -s "$MANIFEST" terraform-registry-manifest.json ||
  fail "staged manifest differs from the manifest configured for publication"

SUMS="terraform-provider-asc_${VERSION}_SHA256SUMS"
[ -f "dist/$SUMS" ] || fail "missing checksum file dist/$SUMS"
(cd dist && sha256_check "$SUMS") >/dev/null ||
  fail "at least one checksum in dist/$SUMS does not verify"
SUMS_LINES="$(wc -l <"dist/$SUMS" | tr -d ' ')"
EXPECTED_SUMS=$(( ${#EXPECTED_PLATFORMS[@]} + 1 ))
[ "$SUMS_LINES" -eq "$EXPECTED_SUMS" ] ||
  fail "dist/$SUMS lists $SUMS_LINES files, expected $EXPECTED_SUMS (archives plus manifest)"
python3 - "dist/$SUMS" "$VERSION" "${EXPECTED_PLATFORMS[@]}" <<'EOF'
import pathlib, sys

path, version, *platforms = sys.argv[1:]
expected = {f"terraform-provider-asc_{version}_{p}.zip" for p in platforms}
expected.add(f"terraform-provider-asc_{version}_manifest.json")
actual = {line.split(maxsplit=1)[1].lstrip("*") for line in pathlib.Path(path).read_text().splitlines()}
if actual != expected:
    sys.exit(f"checksum inventory differs: missing {expected - actual}, unexpected {actual - expected}")
EOF

SIG="dist/${SUMS}.sig"
[ -f "$SIG" ] || fail "missing detached signature $SIG"
gpg --batch --verify "$SIG" "dist/$SUMS" >/dev/null 2>&1 ||
  fail "gpg could not verify $SIG against dist/$SUMS"
if head -c 5 "$SIG" | grep -q -- "-----"; then
  fail "$SIG is ASCII armored; the registry requires a binary detached signature"
fi

cat <<EOF

release-snapshot-check: PASS (version $VERSION)
  - ${#EXPECTED_PLATFORMS[@]} archives: ${EXPECTED_PLATFORMS[*]}
  - $MANIFEST matches the publication manifest and declares version 1 / protocol 6.0
  - dist/$SUMS lists $SUMS_LINES files and every checksum verifies
  - $SIG is a binary detached signature that gpg --verify accepts
  - every archive holds exactly its versioned executable (.exe on Windows), with no manifest inside
EOF
