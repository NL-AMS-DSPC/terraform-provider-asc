# Changelog

All notable changes to this provider are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.1] - 2026-10-23

No changes to the provider. This release checks that a new GitHub release is
published to the Registry automatically.

## [0.2.0] - 2026-10-22

This release follows the infra-gateway on ASC main and contains breaking changes.

### Changed

- `asc_vpc` requires `cidr`, and changing it replaces the VPC. Set it to the
  current CIDR of an existing VPC to keep it.
- Virtual machines and VM groups are created with a 32 GB boot disk. An
  omitted `image` requests `ubuntu-24.04-lts`.
- Creating a VPC waits until it is active, so a virtual machine can be created
  in it in the same apply.
- API error diagnostics quote the response body, capped at 512 bytes, for
  example `infra-gateway returned HTTP 400: {"cidr":"required"}`.

### Removed

- Subnet `type` on `asc_subnet` and `asc_vpc`, and `enable_logging` on
  `asc_virtual_machine`, which ASC no longer has.
- `storage_class` and `access_mode` on `asc_volume` and the `asc_volume` data
  source, which ASC ignores. Remove them from your configuration.

## [0.1.0] - 2026-10-14

First version of `terraform-provider-asc`, and the first published to the
Terraform Registry as `nl-ams-dspc/asc`.

This is a fresh repository, not a rename of `terraform-provider-dspc`. It keeps
the Terraform source address `registry.terraform.io/nl-ams-dspc/asc` and the
`asc_*` resource namespace, which is what makes a `replace-provider` state
surgery unnecessary for anyone coming from the unreleased legacy build. It
speaks wire protocol 6.0 and therefore requires Terraform 1.0 or later.

### Added

- Nine resources: `asc_vpc`, `asc_subnet`, `asc_virtual_machine`,
  `asc_vm_group`, `asc_volume`, `asc_volume_attachment`, `asc_role`,
  `asc_group` and `asc_ip`. All nine are importable; the identifiers are in
  [supported fields](docs/supported-fields.md#import).
- Nine data sources: `asc_skus`, `asc_vpc`, `asc_subnet`,
  `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_role`,
  `asc_group` and `asc_ips`.
- Three API boundaries, and no others. `/api/infra-gateway` serves the
  infrastructure resources behind a `{"data": ...}` envelope; `/api/uam` serves
  roles and groups as bare JSON; `/api/ipam` serves IP reservations. Those
  three prefixes plus the realm token path used to obtain a bearer token are
  the whole of the provider's egress, and the acceptance suite asserts every
  route the provider sends against that allowlist.
- An upgrade path and the evidence for it:
  [the compatibility matrix](docs/compatibility.md),
  [the upgrade runbook](docs/guides/upgrade-runbook.md) and
  [the migration guide](docs/guides/migration.md). `internal/tests/migration_test.go`
  drives the real Terraform CLI over committed legacy-shaped state against an
  httptest gateway and asserts that an unchanged object plans nothing.

### Changed

Against the unreleased legacy build at `main`, which wrote state at this same
source address:

- `asc_block_storage` is now `asc_volume`, and `asc_block_storage_attachment` is
  now `asc_volume_attachment`.
- `asc_vm_group`'s `autoscaling_policy` is now `autoscaling`, and its replica
  bounds widened from 32-bit to 64-bit integers. Rename the configuration
  argument while preserving the policy values before planning.
- The plural data sources `asc_vpcs`, `asc_subnets`, `asc_virtual_machines` and
  `asc_vm_groups` are now singular lookups by name. `asc_skus` keeps its list
  shape.
- `asc_role` and `asc_group` moved from `/api/authorization` to `/api/uam`. A
  role has no `id` attribute, because UAM's two routes disagree about what that
  field holds; a group's `description` and `members` are read-only, because no
  UAM route sets either.
- `tags` no longer forces replacement on `asc_vpc` or `asc_virtual_machine`; a
  tag edit is now an in-place patch.

### Removed

- `asc_group_member` and `asc_group_role` are retired with no replacement and no
  plan: UAM exposes no membership route and no role-assignment route, so the
  capability is absent from the approved boundary rather than merely
  unimplemented here.
- The `asc_block_storage_attachment` data source is retired. infra-gateway
  serves no attachment read route; `asc_volume`'s `attached_to_vm` and `status`
  carry the same facts.
- `asc_security_group`, `asc_security_rule`, `asc_security_group_attachment`,
  `asc_load_balancer`, `asc_mssql`, `asc_postgresql`, `asc_file_storage`,
  `asc_file_storage_access`, `asc_object_storage`, `asc_function`,
  `asc_container` and `asc_cluster`, with their data-source counterparts, are
  deferred. Each is a real capability whose service is still running; what is
  missing is a route on an approved boundary, so none can be restored by
  provider work alone. `asc_load_balancer` is the closest: infra-gateway serves
  its `POST`, `PATCH` and `DELETE` and only the read is missing, and a resource
  that cannot read cannot refresh, detect drift or import.

The disposition of all 41 legacy registrations, with the evidence for each, is
in [legacy registration disposition](docs/legacy-registrations.md).
