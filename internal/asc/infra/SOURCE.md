# infra-gateway OpenAPI source

`openapi.json` is a verbatim copy of the Terraform-facing spec that ASC builds. Do not
edit it by hand; regenerate it in ASC and copy it here.

- ASC target: `//clients/infra-gateway:openapi3_terraform` (`bazel-bin/clients/infra-gateway/openapi3.terraform.json`)
- ASC revision: `main`, commit `5e8ba8ac0e014c33823195748fe58f172e869366`, plus an uncommitted
  allowlist change that adds the VNIC operations. Record the commit that merges it before
  release.
- sha256: `3a8a6d0c788eefb6921c245c96d131d151677a25d868cba1c6513e59692f4a7a`

It includes the required `bootDisk` object on `CreateVMRequest` (ASC commit `12e151d01`), the
required VPC `cidr` and the removal of subnet `type` (ASC PRs #1193 and #1194), and the removal
of `enableLogging` from virtual machines. Virtual machines are created with `vnics`, a list of
VNIC URNs, instead of `vpcID`, and report them in `vnics` on `GET /vms/{vmName}`. The
`CreateVNIC`, `GetVNIC`, `PatchVNIC` (tags), `UpdateVNIC` (name and public IP) and `DeleteVNIC`
operations manage the interfaces.

The allowlist in ASC's `clients/infra-gateway/terraform-operations.json` decides which operations
the spec holds. After copying a new spec, update the revision and sha256 above and run
`make generate`; `make check-generated` verifies the local checksum and generated code.
To verify provenance, rebuild the ASC target at the recorded commit and compare its sha256.
