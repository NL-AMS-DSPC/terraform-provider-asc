# Compatibility and migration matrix

This document freezes what a user can be upgrading *from*, what this provider
answers to, and what the first plan after the switch is allowed to do. It is the
reference the upgrade runbook and the migration tests are both written against;
where a claim here is testable, the test that proves it is named.

Nothing in this document is advice about which resources exist. That is
[legacy registration disposition](legacy-registrations.md) (what each of the 41
legacy registrations became) and [supported fields](supported-fields.md) (what
each shipped resource accepts). This one is about **identity**: addresses,
versions, protocols, state shapes and whether a plan can destroy something.

## The two origins

There are exactly two builds a user's state can have been written by. They are
not variations of one thing; Terraform treats them as unrelated providers,
and that single fact decides both migration paths.

| | Origin A — the published release | Origin B — unreleased `main` |
| --- | --- | --- |
| Repository | `NL-AMS-DSPC/terraform-provider-dspc` at tag `v0.0.1` | `NL-AMS-DSPC/terraform-provider-dspc` at `main` (`1c460cc`) |
| Source address | `registry.terraform.io/NL-AMS-DSPC/dspc` | `registry.terraform.io/nl-ams-dspc/asc` |
| Provider type name | `dspc` | `asc` |
| Published to the public registry | **yes** — v0.0.1, community tier, 2025-10-22 | no |
| Wire protocol | 5.0 | 6.0 |
| Resources registered | 1 (`dspc_virtual_machine`) | 22 |
| How a user got it | `terraform init` from the registry | `dev_overrides`, or a hand-built binary |
| Shares this provider's address | no | **yes** |
| Migration path | `state rm` + `import` | reconcile renamed configuration; refresh same-type state in place; migrate renamed/retired types as described below |

Origin A is on the public registry, which is easy to overlook and changes who
the upgrade guidance is for: it is installable by an ordinary `terraform init`
from any machine, so its users may exist outside this organisation and cannot be
assumed to be reachable internally. Its version list is a single entry, `0.0.1`,
for six platforms.

Origin B is the one that carries destroy risk, precisely because it is *not* a
different provider. It answers to the same source address, the same `asc_*` type
names and the same schema version as this build, so Terraform hands its state
straight over without a word.

## This provider's identity

| Property | Value | Evidence |
| --- | --- | --- |
| Source address | `registry.terraform.io/nl-ams-dspc/asc` | `main.go` `providerserver.ServeOpts.Address` |
| Provider type name | `asc` | `internal/provider/provider.go` `resp.TypeName` |
| Wire protocol | 6.0 | `terraform-registry-manifest.json`; `providerserver.Serve` is protocol 6 |
| Protocol floor / tested CLI | 1.0 / 1.5.7 and 1.16.3 | protocol 6 floor; CI covers the older and current CLI |
| First version | `v0.1.0` | no tag exists yet; see [assumptions](#assumptions) |
| Published to the public registry | not yet | `nl-ams-dspc/asc` answers 404 on the registry versions API |

The address is deliberately unchanged from origin B. Keeping it is what makes a
`replace-provider` state surgery unnecessary, and it is also what makes the
migration tests load-bearing rather than decorative.

## Per-resource identity

Every resource is **schema version 0**, in this provider and in both origins.
Neither origin declares a `Version:` on any schema and neither implements
`UpgradeState` anywhere in its tree, so there is no versioned state upgrader in
play on either side of this upgrade. A future schema version could implement a
version-0 state upgrader; the current provider does not declare one. An upgrader
would not by itself bridge a different source address and resource type.

| Resource | Terraform identity | Import ID | Schema version | Replacement forced by |
| --- | --- | --- | --- | --- |
| `asc_vpc` | `name` | `name` | 0 | `name`; a change to a configured field of an inline subnet |
| `asc_subnet` | `vpc_name` + `name` | `vpc-name:subnet-name` | 0 | `name`, `vpc_name`, `vpc_id`, `cidr`, `type` |
| `asc_virtual_machine` | `name` | `name` | 0 | `name`, `sku_id`, `vpc_id`, `image`, `customization`, `enable_logging` |
| `asc_vm_group` | `name` | `name` | 0 | `name`, `sku_id`, `vpc_id`, `image`, `autoscaling`, `customization`, `tags` |
| `asc_volume` | `name` | `name` | 0 | `name`, `storage_class`, `access_mode` |
| `asc_volume_attachment` | `volume_name` + `vm_name` | `volume-name:vm-name` | 0 | `volume_name`, `vm_name` |
| `asc_role` | `name` | `name` | 0 | `name` |
| `asc_group` | `name` | `name` | 0 | `name` |

A shrinking `asc_volume` is the one case that resolves to neither a change nor a
replacement: `ModifyPlan` rejects it at plan time with a "Volume cannot be
shrunk" diagnostic naming both sizes, because a volume can only grow. 
That is the shape every unrepresentable change should take -- an error
the reader can act on, not a silent destroy.

`asc_virtual_machine`, `asc_vm_group`, `asc_role` and `asc_volume_attachment`
have **no `id` attribute at all**. The framework does not require one, and
inventing one would have meant persisting a value no route returns consistently.
Tooling that assumes every Terraform resource exposes `id` will not find one
here.

## State shape changes from origin B

These are the differences between what origin B wrote into state and what this
provider declares. The framework decodes prior state with
`IgnoreUndefinedAttributes`, so an attribute that origin B wrote and this
provider does not declare is dropped silently rather than raising an error, and
an attribute this provider declares that is absent from prior state arrives as
null.

| Resource | Change | What happens on the first plan |
| --- | --- | --- |
| `asc_vm_group` | `autoscaling_policy` renamed to `autoscaling`; `min_replicas`/`max_replicas` widened from Int32 to Int64 | The old key is dropped and the new one starts null. `Read` refills it from the gateway during refresh, before the plan is computed, so the configured value matches and nothing is proposed. |
| `asc_vm_group` | `os` and `enable_logging` are absent in both schemas | Neither is configurable on a group. |
| `asc_subnet` | `id` was the composite `vpc-name:subnet-name`, now the gateway's uuid | `id` is Computed, so refresh overwrites it. No plan change. |
| `asc_vpc` | inline subnets no longer carry `vpc_name` | Dropped from the nested object; `vpc_id` still carries the linkage. |
| `asc_vpc` | `tags` no longer forces replacement | A tag edit that used to destroy a VPC now patches in place. |
| `asc_virtual_machine` | `tags` no longer forces replacement | Same: a tag edit is now an in-place update. |
| all | attribute *ordering* in the model structs | Irrelevant. State is keyed by name, not position. |

Rename `autoscaling_policy` to `autoscaling` in configuration while retaining
the existing policy values. The old argument is rejected by the current schema.
State keeps the old key until refresh; no state removal is required.

The `asc_vm_group` rename is the one that looked most dangerous and is worth
stating plainly, because the reasoning is not obvious: the renamed attribute
forces replacement, and it starts null after the rename, so a plan computed
against the *stale* state would replace the group and destroy its machines. It
does not, because Terraform refreshes before it plans and the gateway reports the
live policy. The safety therefore depends on the refresh succeeding — which is
why the runbook's rollback conditions treat a failed or skipped refresh
(`-refresh=false`, an unreachable gateway) as a stop, not a warning.

## What the first plan may do

For an object that is running and whose configuration still matches it, the
first plan after the upgrade must propose **nothing**, or fail with an error that
names what is wrong. It may never propose a create, a delete or a replace.

This is tested rather than asserted. `internal/tests/migration_test.go` seeds the
committed legacy-shaped state under `internal/tests/fixtures/` into a real
Terraform working directory, points a `dev_overrides` block at a fresh build of
this provider, and runs the real `terraform` CLI against the same httptest
gateway the rest of the acceptance suite uses. It saves each successful plan
with `plan -out` and parses `show -json`, including the create/delete action
pairs Terraform uses for replacement. Failed plans retain their streamed
diagnostics.

| Test | What it proves |
| --- | --- |
| `TestMigrationFromLegacySameAddressPlansNothing` | For `asc_vpc`, `asc_subnet`, `asc_virtual_machine` and `asc_vm_group`, legacy state plus equivalent configuration using the current attribute names plans zero changes, and the gateway was actually reached — a plan that never refreshed would also propose nothing and would prove nothing. |
| `TestMigrationAssertionDetectsAReplacement` | The guard can fail. A deliberately divergent SKU makes the plan replace the group, and the assertion reports it. Without this the four green cases above would be unfalsifiable. |
| `TestMigrationRunbookBackupAndRestore` | A state backup restored into an isolated local workspace refreshes and plans clean; refresh-only inspection is also rehearsed. |
| `TestMigrationRunbookNoOpApply` | Both saved-plan and automatic no-op applies work under development overrides and send no gateway mutations. |
| `TestMigrationFromReleasedProviderIsNotAnUpgrade` | Terraform never offers origin A's `dspc_virtual_machine` state to this provider: the plan fails asking for `nl-ams-dspc/dspc` and no provider plans a change. |

The route allowlist applies to these tests exactly as it does to the rest of the
suite: `newTestEnvironment` asserts on cleanup that every request stayed inside
`/api/infra-gateway` and `/api/uam`, so a migration path cannot reach a retired
service prefix.

## Why origin A cannot be upgraded in place

Terraform resolves providers by source address. Origin A's state records
`provider["registry.terraform.io/NL-AMS-DSPC/dspc"]` against resource type
`dspc_virtual_machine`. This provider answers to `nl-ams-dspc/asc` and registers
no `dspc_*` type, so Terraform never asks it about those resources — it asks for
the `dspc` provider instead.

A schema upgrader could not close that gap even if one were written. Schema
versions are per resource type within one provider; they do not cross a provider
address or a type-name change. This provider also implements no cross-provider state-move handler for
`dspc_virtual_machine`, so `moved` blocks provide no supported path here. That leaves
`terraform state rm` followed by `terraform import` as the only path from origin
A, by construction rather than by preference.

## Assumptions

- **First version is `v0.1.0`.** No tag exists in this repository yet and no
  release has been cut, so the version in the release proof is a snapshot
  version derived by goreleaser, not a real one. The number is a placeholder
  until the first tag is pushed.
- **Origin A's user population is unknown.** The release is public, so its
  install count is not something this repository can see. The runbook is written
  for the possibility that external users exist.

## Evidence

- Registry state read from `https://registry.terraform.io/v1/providers/...` on
  2026-09-19: `nl-ams-dspc/asc` → HTTP 404; `nl-ams-dspc/dspc` → HTTP 200 with a
  single version `0.0.1`, protocol `5.0`, six platforms, published
  `2025-10-22T18:45:29Z`, source `github.com/NL-AMS-DSPC/terraform-provider-dspc`,
  community tier.
- Origin A: `terraform-provider-dspc` at tag `v0.0.1` — `main.go` `Address`,
  `internal/provider/provider.go` `resp.TypeName`, and
  `internal/provider/virtual_machine_resource.go` whose model is `{id, name}`.
- Origin B: `terraform-provider-dspc` at `1c460cc` — `main.go` `Address`,
  `internal/provider/provider.go` (`Resources()`, `resp.TypeName`) and the
  `ResourceModel` structs under `internal/resources/{vpc,subnet,virtualmachine,vmgroup}/`.
  `grep -rnE "^[[:space:]]*Version:[[:space:]]*[0-9]"` and `grep -rn "UpgradeState"`
  over that tree both return nothing.
- This provider at `bde29b7`: `main.go`, `internal/provider/provider.go`,
  `internal/resources/**` and `terraform-registry-manifest.json`.
