# infra-gateway OpenAPI source

`openapi.json` is a verbatim copy of the Terraform-facing spec that ASC builds. Do not
edit it by hand; regenerate it in ASC and copy it here.

- ASC target: `//clients/infra-gateway:openapi3_terraform` (`bazel-bin/clients/infra-gateway/openapi3.terraform.json`)
- ASC revision: `main`, commit `2331043a1847ee851300a2c439cb83b075e22503`
- sha256: `0a87faabc4fd6431f2069808135f849ce9cabd372976ce401413ae39b2a71dba`

The Terraform spec targets from ASC PR #1076 are merged into `main`, so this is a plain build of
`main`. It includes the required `bootDisk` object on `CreateVMRequest` (ASC commit `12e151d01`).

The allowlist in ASC's `clients/infra-gateway/terraform-operations.json` decides which operations
the spec holds. After copying a new spec, update the revision and sha256 above and run
`make generate`; `make check-generated` verifies the local checksum and generated code.
To verify provenance, rebuild the ASC target at the recorded commit and compare its sha256.
