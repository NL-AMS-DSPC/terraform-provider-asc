#!/usr/bin/env bash
set -euo pipefail
export CHECKPOINT_DISABLE=1

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

NAMESPACE="nl-ams-dspc"
TYPE="asc"
REGISTRY_HOST="registry.terraform.io"

fail() {
  echo "install-check: FAIL: $*" >&2
  exit 1
}

WORKDIR=""
cleanup() {
  [ -n "$WORKDIR" ] && [ -d "$WORKDIR" ] && rm -rf "$WORKDIR"
}
trap cleanup EXIT

command -v terraform >/dev/null 2>&1 || fail "terraform is not on PATH"
command -v go >/dev/null 2>&1 || fail "go is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"

[ -f dist/metadata.json ] ||
  fail "dist/metadata.json is missing; run 'make release-snapshot' first"
VERSION="$(python3 -c 'import json; print(json.load(open("dist/metadata.json"))["version"])')"
[ -n "$VERSION" ] || fail "dist/metadata.json carries no version"

GOOS="$(go env GOOS 2>/dev/null || true)"
GOARCH="$(go env GOARCH 2>/dev/null || true)"
[ -n "$GOOS" ] && [ -n "$GOARCH" ] || fail "could not determine the host platform via go env"
HOST_ARCHIVE="dist/terraform-provider-${TYPE}_${VERSION}_${GOOS}_${GOARCH}.zip"
[ -f "$HOST_ARCHIVE" ] || fail "no release archive for this host platform: $HOST_ARCHIVE"

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/asc-install-check.XXXXXX")"
MIRROR="$WORKDIR/mirror"
CONFIG_DIR="$WORKDIR/config"
mkdir -p "$MIRROR/$REGISTRY_HOST/$NAMESPACE/$TYPE" "$CONFIG_DIR"

cp dist/terraform-provider-"${TYPE}"_"${VERSION}"_*.zip "$MIRROR/$REGISTRY_HOST/$NAMESPACE/$TYPE/"

cat >"$WORKDIR/terraformrc" <<EOF
provider_installation {
  filesystem_mirror {
    path    = "$MIRROR"
    include = ["$REGISTRY_HOST/$NAMESPACE/$TYPE"]
  }
}
EOF

cat >"$CONFIG_DIR/main.tf" <<EOF
terraform {
  required_providers {
    $TYPE = {
      source  = "$NAMESPACE/$TYPE"
      version = "$VERSION"
    }
  }
}
EOF

echo "==> terraform $(terraform version -json | python3 -c 'import json,sys; print(json.load(sys.stdin)["terraform_version"])')"
echo "==> initialising against the packaged mirror for version $VERSION"

INIT_LOG="$WORKDIR/init.log"
if ! TF_CLI_CONFIG_FILE="$WORKDIR/terraformrc" \
  TF_IN_AUTOMATION=1 \
  terraform -chdir="$CONFIG_DIR" init -input=false -no-color >"$INIT_LOG" 2>&1; then
  cat "$INIT_LOG" >&2
  fail "terraform init could not install $NAMESPACE/$TYPE $VERSION from the mirror"
fi

grep -q "$NAMESPACE/$TYPE v${VERSION}" "$INIT_LOG" ||
  {
    cat "$INIT_LOG" >&2
    fail "terraform init did not report installing $NAMESPACE/$TYPE v$VERSION"
  }

LOCK="$CONFIG_DIR/.terraform.lock.hcl"
[ -f "$LOCK" ] || fail "terraform init wrote no dependency lock file"
grep -q "provider \"$REGISTRY_HOST/$NAMESPACE/$TYPE\"" "$LOCK" ||
  fail "$LOCK does not pin $REGISTRY_HOST/$NAMESPACE/$TYPE"
grep -q "version *= *\"$VERSION\"" "$LOCK" ||
  {
    cat "$LOCK" >&2
    fail "$LOCK does not resolve $NAMESPACE/$TYPE at $VERSION"
  }
grep -q "h1:" "$LOCK" || fail "$LOCK records no h1 hash for the provider archive"

INSTALLED="$CONFIG_DIR/.terraform/providers/$REGISTRY_HOST/$NAMESPACE/$TYPE/$VERSION/${GOOS}_${GOARCH}"
[ -d "$INSTALLED" ] || fail "expected the unpacked provider at $INSTALLED"
PROVIDER_BINARY="terraform-provider-${TYPE}_v${VERSION}"
[ "$GOOS" != windows ] || PROVIDER_BINARY="${PROVIDER_BINARY}.exe"
[ -x "$INSTALLED/$PROVIDER_BINARY" ] ||
  fail "unpacked provider is not named $PROVIDER_BINARY under $INSTALLED"

export TF_CLI_CONFIG_FILE="$WORKDIR/terraformrc" TF_IN_AUTOMATION=1
terraform -chdir="$CONFIG_DIR" providers >"$WORKDIR/providers.txt"
grep -Fq "provider[$REGISTRY_HOST/$NAMESPACE/$TYPE] $VERSION" "$WORKDIR/providers.txt" ||
  fail "terraform providers did not resolve the expected address and version"

terraform -chdir="$CONFIG_DIR" providers schema -json >"$WORKDIR/schema.json"
python3 - "$WORKDIR/schema.json" "$REGISTRY_HOST/$NAMESPACE/$TYPE" <<'EOF'
import json, sys

with open(sys.argv[1]) as handle:
    schemas = json.load(handle)["provider_schemas"]
address = sys.argv[2]
if set(schemas) != {address}:
    sys.exit(f"unexpected provider addresses: {list(schemas)}")
schema = schemas[address]
resources = {"asc_vpc", "asc_subnet", "asc_virtual_machine", "asc_vm_group",
             "asc_volume", "asc_volume_attachment", "asc_role", "asc_group"}
data_sources = resources - {"asc_volume_attachment"} | {"asc_skus"}
if set(schema.get("resource_schemas", {})) != resources:
    sys.exit("installed provider exposes an unexpected resource inventory")
if set(schema.get("data_source_schemas", {})) != data_sources:
    sys.exit("installed provider exposes an unexpected data-source inventory")
EOF

cat <<EOF

install-check: PASS (version $VERSION)
  - packaged filesystem mirror built from the release zips, no network, no credentials
  - terraform init installed $NAMESPACE/$TYPE v$VERSION for ${GOOS}_${GOARCH}
  - .terraform.lock.hcl pins $REGISTRY_HOST/$NAMESPACE/$TYPE at $VERSION with an h1 hash
  - unpacked executable is $PROVIDER_BINARY
  - terraform providers confirms the address and version; the installed binary serves all eight resources and eight data sources
EOF
