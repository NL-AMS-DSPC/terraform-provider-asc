# infra-gateway OpenAPI source

`openapi.json` is a verbatim copy of the Terraform-facing spec that ASC builds. Do not
edit it by hand; regenerate it in ASC and copy it here.

- ASC target: `//clients/infra-gateway:openapi3_terraform` (`bazel-bin/clients/infra-gateway/openapi3.terraform.json`)
- ASC revision: `main`, commit `e3802ca9dd54139f0f4a752f5ce481b489ba1e67`
- sha256: `1bd58396a87dd8bfeeda412e98c87ab5f4eb006270c16e57a12671902bf22bb0`

The Terraform spec targets from ASC PR #1076 are merged into `main`, so this is a plain build of
`main`. It includes the required `bootDisk` object on `CreateVMRequest` (ASC commit `12e151d01`),
the required VPC `cidr` and the removal of subnet `type` (ASC PRs #1193 and #1194), and the
removal of `enableLogging` from virtual machines.

The allowlist in ASC's `clients/infra-gateway/terraform-operations.json` decides which operations
the spec holds. After copying a new spec, update the revision and sha256 above and run
`make generate`; `make check-generated` verifies the local checksum and generated code.
To verify provenance, rebuild the ASC target at the recorded commit and compare its sha256.
