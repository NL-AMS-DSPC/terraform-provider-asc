# Changelog

All notable changes to this provider are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-11-26

This release follows ASC's move from a VPC ID to virtual network interfaces on
virtual machines, removes VM groups, and contains breaking changes.

### Added

- `asc_vnic` resource: a virtual network interface in a subnet, with its
  private IP, MAC and attachment status. Its name and tags update in place;
  changing `subnet_urn` replaces it. Import it by the UUID that ends its URN.
- `vnics` on the `asc_virtual_machine` data source, in attach order.

### Changed

- `asc_virtual_machine` requires `vnics`, the URNs of the VNICs to attach,
  instead of `vpc_id`. Changing the list replaces the machine. `vpc_id` is
  now read-only. For an existing machine, replace `vpc_id` with its current
  VNICs: the first refresh reads them into state, so a matching list plans no
  change. Creating a machine waits until all of its VNICs are attached, and a
  refresh detects VNICs attached or detached outside Terraform.

### Removed

- The `asc_vm_group` resource and data source. Remove them from your
  configuration and state before upgrading, either with a `removed` block with
  `lifecycle { destroy = false }` or with `terraform state rm`.

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

It uses the Terraform source address `registry.terraform.io/nl-ams-dspc/asc` and
the `asc_*` resource namespace. It speaks wire protocol 6.0 and therefore
requires Terraform 1.0 or later.

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
- `internal/tests/migration_test.go` drives the real Terraform CLI over
  committed state fixtures against an httptest gateway and asserts that an
  unchanged object plans nothing.

### Not supported

- `asc_group_member` and `asc_group_role`: UAM exposes no membership route and
  no role-assignment route, so the capability is absent from the approved
  boundary rather than merely unimplemented here.
- The `asc_block_storage_attachment` data source. infra-gateway serves no
  attachment read route; `asc_volume`'s `attached_to_vm` and `status`
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
