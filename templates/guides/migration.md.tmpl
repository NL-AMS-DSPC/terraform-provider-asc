---
page_title: "Migrating from terraform-provider-dspc"
subcategory: ""
description: |-
  What the legacy provider's 41 registrations become in terraform-provider-asc, and what to do with each one.
---

# Migrating from terraform-provider-dspc

`terraform-provider-asc` is a fresh repository, not a rename or a cleanup of the older
`terraform-provider-dspc`. The old repository remains read-only legacy and migration evidence.

The legacy provider registered 22 resources and 19 data sources against eleven service prefixes.
This provider registers eight resources and eight data sources against two: `/api/infra-gateway`
and `/api/uam`. This guide says what to do per capability. The per-registration matrix, with the
evidence for every disposition, is in
[legacy registration disposition](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/legacy-registrations.md);
this guide does not repeat its 41 rows.

## Before you start

Which state you hold decides what each instruction below means. There are two origins: the
published `NL-AMS-DSPC/dspc` v0.0.1 release, which registers one resource,
`dspc_virtual_machine`, at a different source address; and the unreleased legacy build at `main`,
which registers the 22 resources this guide's dispositions describe, at *this* provider's own
source address. Except where a section says otherwise, the dispositions below are about state
written by the unreleased build at `main`, because it is the only origin that ever held these
registrations.

The identities, protocols and schema versions behind that split are in
[the compatibility matrix](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/compatibility.md).
The ordered procedure, with the stop conditions that abort it, is in
[the upgrade runbook](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/guides/upgrade-runbook.md).

## What is preserved

Provider type and resource names keep the `asc` / `asc_*` spelling, and the registry source stays
`NL-AMS-DSPC/asc`, so the Terraform source address does not change and no `replace-provider` state
surgery is required.

## The two boundaries

Every request obtains an OAuth client-credentials token from the configured authentication endpoint
and sends the configured namespace as `X-Tenant`.

- `/api/infra-gateway` serves the infrastructure resources and decodes a `{"data": ...}` envelope.
  HTTP 404 and 409 are mapped to typed errors.
- `/api/uam` serves roles and groups. It answers bare JSON with no envelope, and a role or group
  that does not exist answers HTTP 502 rather than 404, so existence is decided from a list route.

Those two prefixes and the realm token path are the whole of the provider's egress. The acceptance
suite records every route the provider sends and fails the build on anything outside that
allowlist, so no legacy service prefix can be reintroduced by accident.

## Shipped, same address

`asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group` and the `asc_skus` data source keep
their legacy names. What to do with the existing state entries depends on which build wrote them,
and the difference is forced rather than chosen.

**State written by the unreleased legacy build at `main`.** It records this provider's own source
address, `registry.terraform.io/nl-ams-dspc/asc`, the same `asc_*` type names and the same schema
version 0, so Terraform does not treat it as a different provider: it hands the state over, the
framework decodes it, and the first plan is a refresh. **Do not remove and re-import these
entries.** `internal/tests/migration_test.go` seeds exactly this state for all four resources and
asserts the first plan proposes nothing against a mock api. For VM groups, first rename
`autoscaling_policy` to `autoscaling` in configuration while preserving its values; the old
argument is rejected. The state fixture retains the old spelling until refresh.
That safety depends on the refresh reaching the api, because `asc_vm_group`'s renamed
`autoscaling` attribute starts null and forces replacement until `Read` refills it; the
[upgrade runbook](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/guides/upgrade-runbook.md)
treats a failed or skipped refresh as a stop for that reason.

**State written by the published `NL-AMS-DSPC/dspc` v0.0.1 release.** It holds only
`dspc_virtual_machine`, under a different source address and a different type name, so Terraform
never offers it to this provider and no schema upgrader could reach it. Remove the entry and import
the live machine:

```shell
terraform state rm dspc_virtual_machine.web
terraform import asc_virtual_machine.web web-01
```

`asc_skus` is a data source either way and needs nothing.

## Shipped, renamed

| Legacy address | New address |
| --- | --- |
| `asc_block_storage` | `asc_volume` |
| `asc_block_storage_attachment` | `asc_volume_attachment` |

The type name changed, so unlike the four resources above these are not carried over by the
refresh: this provider registers no `asc_block_storage`, and a state entry whose type the provider
does not register fails every plan.

```shell
terraform state rm asc_block_storage.data
terraform state rm asc_block_storage_attachment.data
terraform import asc_volume.data data-01
terraform import asc_volume_attachment.data data-01:web-01
```

## Shipped, reshaped

The legacy plural data sources `asc_vpcs`, `asc_subnets`, `asc_virtual_machines` and
`asc_vm_groups` each returned a list attribute. Their replacements `asc_vpc`, `asc_subnet`,
`asc_virtual_machine` and `asc_vm_group` resolve a single object by name, so a configuration that
indexed into the list selects by name instead. Data sources hold no durable state, so a
`terraform plan` after the address change is enough. `asc_skus` keeps its list shape.

The `asc_block_storage_attachment` data source is gone: infra-gateway has no attachment read route.
Read the same facts from `asc_volume`, whose `attached_to_vm` names the machine and whose `status`
reports whether the attachment has converged.

## Roles and groups, now on UAM

`asc_role` and `asc_group` return as resources and as data sources, on `/api/uam` rather than the
legacy `/api/authorization`. Both are addressed by name on every route, the deletes included, so
both import by name:

```shell
terraform import asc_role.network_admin network-admin
terraform import asc_group.platform platform-team
```

State written by the unreleased legacy build at `main` carries over, because that build registered
`asc_role` and `asc_group` under the same type names at the same source address. These two are not
covered by the migration tests, so treat the first plan as the check: a proposed replacement of a
role or group is a stop, not something to apply through. The published v0.0.1 release registered
neither, so there is no legacy role or group state to move from it.

Three limits change what a configuration may say:

- A role's Terraform identity is its **name**. The legacy resource persisted UAM's `id`, but that
  field holds the role's name on one route and a database uuid on another, so this provider does
  not keep it and there is no `id` attribute. `name` forces replacement; only `actions` updates in
  place. A role's actions are sent as `actions` and read back as `composites`.
- A group has no update route at all. `name` forces replacement, and `description` and `members`
  are read-only, because no UAM route sets a description or adds a member. A configuration that
  set either on the legacy resource must drop those arguments before the first plan.
- A missing role or group surfaces as HTTP 502, not 404. The provider decides existence from the
  list route, so a resource deleted outside Terraform is still removed from state correctly, but an
  error naming a 502 is reported as an error and not silently treated as a deletion.

Per-attribute detail is in
[supported fields](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/supported-fields.md).

## Retired: `asc_group_member` and `asc_group_role`

These two have no replacement and no plan. UAM exposes no route that adds or removes a group
member, and no route that assigns a role to a user, a group or an API client. The capability is
absent from the approved boundary, so it is not waiting on provider work.

For every such entry:

```shell
terraform state rm asc_group_member.example
terraform state rm asc_group_role.example
```

Then delete the corresponding configuration blocks. Leaving the entries in state makes every plan
fail once the legacy provider is removed.

`terraform state rm` forgets a resource; it does not revoke anything. The memberships and role
assignments themselves are untouched, so live access is unaffected — it simply stops being managed
by Terraform. Administer membership and role assignment through UAM directly until UAM exposes
routes for them.

## Deferred

`asc_security_group`, `asc_security_rule`, `asc_security_group_attachment`, `asc_load_balancer`,
`asc_mssql`, `asc_postgresql`, `asc_file_storage`, `asc_file_storage_access`, `asc_object_storage`,
`asc_function`, `asc_container`, `asc_cluster` and their data-source counterparts are real
capabilities whose services are still running. What is missing is a route on an approved boundary,
so none of them can be restored by provider work alone.

The migration is the same for each: `terraform state rm` the entry, delete the configuration block,
and manage the object outside Terraform. Removing an entry from state does not destroy the object.

`asc_load_balancer` is the closest to returning. infra-gateway already serves `POST`, `PATCH` and
`DELETE /load-balancers` and only the read is missing; a resource that cannot read cannot refresh,
detect drift or import, which is why it is absent rather than shipped write-only.
