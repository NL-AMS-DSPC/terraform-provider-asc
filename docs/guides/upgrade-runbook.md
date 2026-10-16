---
page_title: "Upgrade runbook"
subcategory: ""
description: |-
  The procedure for moving an existing workspace onto terraform-provider-asc without destroying running infrastructure, with the stop conditions that abort it.
---

# Upgrade runbook

This is the procedure an operator runs. It assumes the workspace is already
managing live objects and that the goal is to change the provider underneath it
without changing what is running.

Read it end to end before running the first command. The two origins it branches
on are not variations of one another, and an instruction from the wrong branch
destroys and re-imports working infrastructure for no reason.

What each origin is, what this provider answers to, and why the first plan is
allowed to do what it does are in
[the compatibility matrix](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/compatibility.md).
What each legacy capability becomes is in
[the migration guide](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/guides/migration.md).
This runbook does not repeat either; it sequences them.

## What this runbook is founded on, and what it is not

The migration behaviour this runbook relies on is tested in
`internal/tests/migration_test.go` against an **httptest mock gateway**, not
against a live ASC deployment. The mock serves the objects the fixture state
describes, so the tests prove what Terraform and the provider do with legacy
state and a gateway response of that shape. They prove nothing about how a real
ASC environment answers.

Nothing here has been verified against a real ASC deployment. No release has
been published to the Terraform Registry, none has been signed with the real
signing key, and no version has been installed from the registry. What has been
proven locally is that a goreleaser snapshot signed with a throwaway key
produces the artifacts the registry looks for (`make release-snapshot`) and that
those artifacts install through an offline filesystem mirror (`make
install-check`). Treat every step below as reviewed and reasoned, and the live
behaviour as unverified until you have run it on a workspace you can afford to
roll back.

## Preconditions

- **Terraform 1.5 or later.** The provider speaks protocol 6, which Terraform
  has supported since 1.0, and CI tests 1.5.7 and 1.16.3. Other CLI versions need
  the same acceptance checks before rollout.
  Check with `terraform version`.
- **Go 1.27.1 or later**, and only if you build the provider from source. The
  module declares `go 1.27.1`. An operator installing a released archive needs
  no Go toolchain.
- **Access to the gateway from wherever you run Terraform.** Every step below
  depends on a refresh reaching `/api/infra-gateway`, for the reason set out
  under [stop conditions](#why-a-skipped-refresh-is-the-condition-that-matters-most).
- **A workspace you can afford to roll back**, since nothing here has been run
  against a real ASC deployment.

## Step 0 — decide which origin you are on

Two builds can have written the state you hold. Terraform resolves providers by
source address, so the address recorded in state decides everything that
follows.

```sh
terraform state pull > /tmp/origin-check.json
python3 - /tmp/origin-check.json <<'EOF'
import collections, json, sys
state = json.load(open(sys.argv[1]))
counts = collections.Counter(
    (r["provider"], r["type"]) for r in state.get("resources", [])
)
for (provider, kind), count in sorted(counts.items()):
    print(f"{count:4d}  {kind:32s}  {provider}")
EOF
```

`terraform providers` gives the same answer from the configuration side and is
worth running alongside it, because a configuration can require a provider that
state does not yet mention.

```sh
terraform providers
```

Read the result:

| What the output shows | Origin | Branch to follow |
| --- | --- | --- |
| `provider["registry.terraform.io/NL-AMS-DSPC/dspc"]` against `dspc_virtual_machine` | **A**, the published `NL-AMS-DSPC/dspc` v0.0.1 release | [Procedure A](#procedure-a--origin-a-state-rm-and-import) |
| `provider["registry.terraform.io/nl-ams-dspc/asc"]` against `asc_*` types | **B**, the unreleased legacy build at `main` | [Procedure B](#procedure-b--origin-b-refresh-in-place) |
| Both | Both, in one state | Run Procedure B first, then Procedure A for the `dspc_*` entries |

Terraform lower-cases source addresses in some output, so
`NL-AMS-DSPC/dspc` and `nl-ams-dspc/dspc` are the same address. What
distinguishes the origins is `dspc` against `asc`, not the case.

Origin A is on the public registry, so a workspace on it may belong to someone
outside this organisation and cannot be assumed to be internal. Origin B was
never released; it was installed through `dev_overrides` or as a hand-built
binary.

## Step 1 — take a restorable state backup

Do this before changing a single line of configuration.

```sh
umask 077
backup="asc-upgrade-backup-$(date -u +%Y%m%dT%H%M%SZ).json"
terraform state pull > "$backup"
```

`terraform state pull` works for a local backend and for every remote backend,
which is why it is used here rather than copying `terraform.tfstate`. For a
remote backend it fetches the state the backend currently holds; it does not
lock it, so take the backup at a moment when nobody else is applying. If the
backend is versioned (an S3 bucket with versioning, or Terraform Cloud's own
state history), that version history is a second copy and is worth noting down
by version id — but do not treat it as a substitute, because restoring from it
is a different procedure per backend.

Verify the backup before trusting it. An empty or truncated file is the failure
mode that matters, and it is silent:

```sh
# Keep the exact filename from the preceding command; do not select a wildcard.
python3 - "$backup" <<'EOF'
import json, sys
state = json.load(open(sys.argv[1]))
resources = state.get("resources", [])
instances = sum(len(r.get("instances", [])) for r in resources)
print("serial", state["serial"], "lineage", state["lineage"])
print("resources", len(resources), "instances", instances)
assert resources, "state backup lists no resources"
EOF
```

The assertion is the point: a backup that parses but holds no resources restores
nothing. Record the `serial` and `lineage` printed here; you need both to judge
a restore later.

## Step 2 — pin the provider version

Pin so that the upgrade happens when you decide it happens, and not on an
unrelated `terraform init -upgrade` weeks later.

```hcl
terraform {
  required_providers {
    asc = {
      source  = "nl-ams-dspc/asc"
      version = "0.1.0"
    }
  }
}
```

`0.1.0` is the assumed first version. **No tag exists in this repository yet and
`nl-ams-dspc/asc` is not on the public registry**, so this pin cannot resolve
today. Until the first release is published there are two ways to run this
build, and both are what the migration tests and the install check use:

```hcl
# ~/.terraformrc or the file named by TF_CLI_CONFIG_FILE — a local build.
provider_installation {
  dev_overrides {
    "nl-ams-dspc/asc" = "/absolute/path/to/directory/holding/the/binary"
  }
  direct {}
}
```

```hcl
# Or a filesystem mirror built from the release archives, which is what
# scripts/install-check.sh proves installable offline.
provider_installation {
  filesystem_mirror {
    path    = "/absolute/path/to/mirror"
    include = ["registry.terraform.io/nl-ams-dspc/asc"]
  }
}
```

A `dev_overrides` block bypasses provider version and checksum selection for
commands such as plan and apply. Terraform 1.5.7 supports saved plans and both
apply modes with an override; the mock runbook tests exercise these paths. It
does not prove which released package an operator installed. Use the filesystem
mirror for packaged acceptance and a normal pinned installation for rollout.
With an override, skip provider installation via `init` in an already initialized
workspace: `init` still tries to resolve the unpublished address. See
[Terraform development overrides](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers).

Before you change anything, pin the build you are on now, so that rolling back
is a one-line edit rather than an archaeology exercise.

- **Origin A**: pin `version = "0.0.1"` on `source = "NL-AMS-DSPC/dspc"`. That
  release is on the public registry, so this pin resolves.
- **Origin B**: there is no released version to pin. Keep the binary you are
  running now — copy it somewhere permanent and note its path — because the only
  rollback is to point `dev_overrides` back at it. If you cannot produce that
  binary again, you have no rollback for the provider itself, only for the
  state.

Commit the pin, and commit `.terraform.lock.hcl` with it.

## Step 3 — dry run

Apply required schema spelling changes first, preserving the configured values.
For VM groups, rename `autoscaling_policy` to `autoscaling`. Keep infrastructure
changes, such as SKU or tag edits, out of the migration plan.

```sh
terraform init -input=false
terraform plan -input=false -lock-timeout=120s -out=tfplan
terraform show -json tfplan > tfplan.json
```

The saved-plan commands also work under `dev_overrides`; omit the `init` above
for an already initialized development workspace. To inspect refresh events in
addition to the saved plan, use the streamed log:

```sh
set -o pipefail
terraform plan -json -input=false -lock-timeout=120s -out=tfplan | tee plan.jsonl
grep '"planned_change"' plan.jsonl
```

Decide from the actions, not from the prose:

```sh
python3 - tfplan.json <<'EOF'
import json, sys
plan = json.load(open(sys.argv[1]))
bad = []
for change in plan.get("resource_changes", []):
    actions = change["change"]["actions"]
    if actions == ["no-op"] or actions == ["read"]:
        continue
    print(change["address"], actions)
    if {"create", "delete"} & set(actions):
        bad.append((change["address"], actions))
print("DESTRUCTIVE" if bad else "clean")
EOF
```

`create` and `delete` are the two actions that matter. Terraform reports a
replacement as `["delete", "create"]` or `["create", "delete"]` as well as
`replace`, so matching on `replace` alone misses the shape that actually
destroys a machine. The migration tests reject every spelling for the same
reason.

| Plan output | Meaning | Action |
| --- | --- | --- |
| `No changes. Your infrastructure matches the configuration.` | The state was adopted and refreshed cleanly | Proceed |
| Only in-place updates, on objects whose configuration you deliberately changed | Ordinary drift or an edit you made | Proceed, after reading each one |
| Any create, delete or replace on an object you did not change | **Stop** | See [stop conditions](#stop-conditions) |
| An error naming a provider you did not expect | **Stop** | You are on the wrong branch; go back to step 0 |

The plan proposing nothing is only meaningful if the refresh actually happened.
A plan that never reached the gateway also proposes nothing, and proves nothing.
Confirm the refresh ran — `terraform plan` prints a `Refreshing state...` line
per resource, and `plan.jsonl` carries `refresh_start` messages — before reading
an empty plan as success.

## Stop conditions

Each of these is an observable in the output, not a judgement call. Any one of
them aborts the upgrade.

| Observable | Why it aborts |
| --- | --- |
| Any request outside `/api/infra-gateway`, `/api/uam` and the configured realm token path | The provider crossed the approved service boundary. Stop and retain a redacted request log. |
| You passed `-refresh=false`, or set `TF_CLI_ARGS_plan` to include it | The plan is computed against stale state. See below. |
| A refresh error: `Error: Unable to Refresh Resource`, a connection refused, a timeout, or any 5xx from the gateway | Same as above — an object that did not refresh is planned from what state says, not from what exists. |
| The plan replaces an `asc_vm_group` and the diff shows `autoscaling` forcing replacement | The refresh did not refill the renamed attribute. Applying this destroys the group's machines. |
| The plan proposes a create, delete or replace for any object whose configuration you did not change | Whatever the reason, it is not the upgrade doing nothing. |
| `Error: Missing required provider` or a plan asking for `nl-ams-dspc/dspc` | Origin A state in a workspace you were treating as origin B. |
| `terraform init` reports a provider version other than the one you pinned | The pin is not in effect, so what you are about to apply is not what you reviewed. |
| A configuration error naming `description` or `members` on `asc_group` | The configuration still sets attributes this provider makes read-only. Fix the configuration first; do not work around it. |

### Why a skipped refresh is the condition that matters most

`asc_vm_group`'s scaling attribute was renamed from `autoscaling_policy` to
`autoscaling`, and it forces replacement. The framework decodes prior state with
`IgnoreUndefinedAttributes`, so the old key is dropped silently and the new one
starts **null**.

A plan computed against that stale state would compare a configured
`autoscaling` block against null, see a difference on a `RequiresReplace`
attribute, and propose to replace the group — which destroys the machines in it.

It does not, because Terraform refreshes before it plans: `Read` fetches the
live policy from the gateway and writes it into the refreshed state, and the
plan is computed against that. The rename is safe *because of the refresh*, not
in spite of it. Remove the refresh — with `-refresh=false`, an unreachable
gateway, or a gateway answering errors — and the safety is gone. That is why a
failed or skipped refresh is a stop, not a warning.

## Staged rollout gates

Do not upgrade every workspace at once. Each gate is a workspace class, and each
one has to come back clean before the next begins.

| Gate | Workspace | Passes when |
| --- | --- | --- |
| 1 | A throwaway workspace you created for this, managing objects you are willing to lose | Steps 0 to 3 complete, the dry run proposes nothing, and an `apply` of the empty plan changes nothing |
| 2 | One non-production workspace with the smallest real surface you have | The dry run proposes nothing, and a deliberate change (a tag edit) plans as an in-place update rather than a replacement |
| 3 | The remaining non-production workspaces | Every dry run proposes nothing, with no stop condition seen in any of them |
| 4 | Production, one workspace at a time, smallest first | Same as gate 3, and each workspace is left with a clean plan before the next is started |

Gate 2's tag edit is worth running deliberately: `tags` no longer forces
replacement on `asc_vpc` or `asc_virtual_machine`, and seeing that land as an
update is the cheapest live confirmation that this provider, and not a cached
older build, is the one answering.

Stop the whole rollout on the first stop condition in any gate. A stop condition
in gate 3 does not mean gate 4 is safe with more care; it means something is
wrong with the upgrade and the remaining workspaces are still intact.

## Support and escalation

**Owner: not yet assigned.** Record the name or rota here before this runbook is
handed to anyone, and make it the first thing an operator reads when a stop
condition fires. An operator who hits one of the conditions above should stop and
escalate rather than improvise, because most of them mean the plan is being
computed against state that does not describe what is running.

## Rollback

Restore the backup only if no apply has run and no remote object has changed
since the backup. Stop other writers first. After any apply (including a failed
or cancelled apply), inventory the actual objects and reconcile/import them
under the chosen provider; do not push the pre-apply backup over the new state.
Rollback restores what Terraform believes. It does not restore destroyed objects.
The state-push steps below require the real workspace and operator verification
of its backend, lineage and serial; the local rehearsal uses a separate empty
workspace and never forces an overwrite.

1. Re-pin the old provider. Origin A: `NL-AMS-DSPC/dspc` at `0.0.1`. Origin B:
   point `dev_overrides` back at the binary you saved in step 2. Then
   `terraform init -input=false` for a normal installation. With an origin-B
   development override on an initialized workspace, skip provider installation.
2. Restore the state backup.

   ```sh
   terraform state push asc-upgrade-backup-<timestamp>.json
   ```

   Terraform refuses a push whose `serial` is lower than the current one, and
   refuses a push whose `lineage` differs. Both refusals are correct and both
   are overridable with `-force`; use `-force` only after you have confirmed
   with `terraform state pull` that the state you are overwriting is the one the
   upgrade wrote, and never to reconcile a differing lineage, which means the
   backup came from a different workspace.
3. Plan again on the old provider and confirm it proposes nothing.

**What rollback cannot undo**: anything already applied. Restoring state makes
Terraform forget a destroy; it does not bring back the object. A destroyed
virtual machine, VPC or volume is gone, along with whatever was on it. Restoring
a backup that still lists a destroyed object leaves you with state describing
something that no longer exists, and the next plan proposes to create it. This
is the whole reason the dry run is a separate step with its own stop conditions:
the only reliable rollback is the one you never needed.

## Procedure A — origin A, `state rm` and import

Applies to state written by the published `NL-AMS-DSPC/dspc` v0.0.1 release. It
holds one resource type, `dspc_virtual_machine`, whose whole model is `{id,
name}`.

There is no in-place path here, by construction rather than by preference.
Terraform resolves providers by source address; this provider answers to
`nl-ams-dspc/asc` and registers no `dspc_*` type, so Terraform never offers that
state to it. A schema upgrader cannot cross a provider address or a type-name
change. This provider implements no cross-provider state-move handler for
`dspc_virtual_machine`, so a `moved` block is not a supported path here. `TestMigrationFromReleasedProviderIsNotAnUpgrade` pins this: the plan
fails asking for `nl-ams-dspc/dspc`, and no provider plans a change.

Origin A is also protocol 5.0 against this provider's 6.0. Protocol 6 requires
Terraform 1.0 or later; check with `terraform version` before you start.

1. Record what the old state holds, one line per machine. This is the only
   inventory you get once the entries are removed.

   ```sh
   terraform state list | grep '^dspc_virtual_machine\.'
   terraform state show dspc_virtual_machine.web
   ```

2. Write the new configuration. The legacy model carried a name and nothing
   else, so every other required argument has to come from the live object:
   `asc_virtual_machine` requires `name`, `sku_id` and `vpc_id`. Get them from
   the environment before importing, not after — `sku_id` and `vpc_id` both
   force replacement, so an import against a configuration that guesses them
   plans a replacement of a running machine.

3. Remove the old entry and import the live object. `terraform state rm` forgets
   a resource; it does not destroy it.

   ```sh
   terraform state rm dspc_virtual_machine.web
   terraform import asc_virtual_machine.web web-01
   ```

   The import ID is the machine's name. Import IDs for every resource are in
   [supported fields](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/supported-fields.md#import):
   `asc_vpc`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_role`
   and `asc_group` import by `name`; `asc_subnet` by `vpc-name:subnet-name` and
   `asc_volume_attachment` by `volume-name:vm-name`.

4. Read back what the import adopted and reconcile the configuration to it.

   ```sh
   terraform state show asc_virtual_machine.web
   ```

   `customization` is write-only and is never served back, so an import leaves
   it null. Supply it from the configuration if you manage it; it forces
   replacement, so adding it to the configuration of an imported machine plans a
   replacement.

5. Plan. The target is no changes. Treat anything else as a
   [stop condition](#stop-conditions) and reconcile the configuration until the
   plan is empty, rather than applying through it.

6. Remove the `dspc` provider from `required_providers` once no `dspc_*` entry
   remains in state.

The mock tests cover removing a legacy VM state entry, importing the live VM
under the new type, and checking an empty plan. The complete
origin-A sequence against a real legacy VM still requires the disposable stack;
those tests do not establish that a real object has all required fields.

## Procedure B — origin B, refresh in place

Applies to state written by the unreleased legacy build at `main`. It records
this provider's own source address, `registry.terraform.io/nl-ams-dspc/asc`, the
same `asc_*` type names and the same schema version 0, so Terraform does not
consider it a different provider. It hands the state over, the framework decodes
it, and the first plan is a refresh.

**Do not `terraform state rm` the four same-named resources.** `asc_vpc`,
`asc_subnet`, `asc_virtual_machine` and `asc_vm_group` carry over. Removing and
re-importing a working object here would take a path with real failure modes —
a botched import ID, an unreconstructable `customization`, a `vpc_id` the group
read cannot recover — in place of a path that does nothing.

1. Complete steps 0 to 2. Reconcile the entries below before the first plan.
   The four same-named resources need no state surgery.

2. Reconcile the entries this provider no longer registers, *before* the first
   plan, because a state entry whose type the configured provider does not
   register makes every plan fail. Per capability, with the reasoning, in
   [the migration guide](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/guides/migration.md):

   - **Renamed**: `asc_block_storage` and `asc_block_storage_attachment` become
     `asc_volume` and `asc_volume_attachment`. The type name changed, so these
     are `state rm` plus `import`.
   - **Retired**: `asc_group_member` and `asc_group_role` — `state rm`, then
     delete the configuration blocks. Nothing is revoked; the memberships and
     assignments stay exactly as they are and simply stop being managed by
     Terraform.
   - **Deferred**: the security, load-balancer, database, file and object
     storage, function, container and cluster registrations — the same `state
     rm` and block deletion. Removing an entry from state does not destroy the
     object.
   - **Reshaped data sources**: the plural `asc_vpcs`, `asc_subnets`,
     `asc_virtual_machines` and `asc_vm_groups` become singular lookups by name.
     Data sources hold no durable state, so a configuration edit is the whole of
     it.

3. Reconcile configuration with the new schema while preserving input values:

   - Rename `asc_vm_group`'s `autoscaling_policy` argument to `autoscaling`
     in configuration, keeping the policy values unchanged. The old argument
     is unsupported. Refresh fills the renamed state attribute. See [why a skipped refresh
     matters](#why-a-skipped-refresh-is-the-condition-that-matters-most).
   - `asc_subnet`'s `id` was the composite `vpc-name:subnet-name` and is now the
     gateway's uuid. It is computed, so the refresh overwrites it.
   - `asc_vpc`'s inline subnets no longer carry `vpc_name`; `vpc_id` still
     carries the linkage.
   - `tags` no longer forces replacement on `asc_vpc` or
     `asc_virtual_machine`. A tag edit that used to destroy and recreate now
     patches in place.
   - `asc_vpc` requires `cidr`. Set it to the VPC's current CIDR, which
     `terraform state show` prints; any other value replaces the VPC.
   - Subnet `type` and `asc_virtual_machine`'s `enable_logging` are deprecated
     and ignored. Remove them; removing them plans no change.
   - Attributes origin B wrote that this provider does not declare are dropped
     silently rather than erroring, and attributes this provider declares that
     prior state lacks arrive null.

4. `asc_role` and `asc_group` also carry over, for the same reason the other
   four do: same address, same type names. Their schemas changed more than the
   infrastructure resources' did — there is no `id` attribute on a role, and a
   group's `description` and `members` are read-only because no UAM route sets
   them. A configuration that sets either must drop the argument before the
   first plan. **This pair is not covered by the migration tests**; treat the
   dry run as the check and a proposed replacement of a role or group as a stop.

5. Inspect a refresh-only plan, then run the normal saved-plan dry run from
   step 3. A refresh-only plan alone does not expose replacement caused by the
   configuration, so both checks matter. Do not apply the refresh plan as part
   of this inspection.

   ```sh
   terraform plan -refresh-only -input=false -lock-timeout=120s -out=refresh.tfplan
   terraform show -json refresh.tfplan > refresh.tfplan.json
   ```

   An unchanged object must plan nothing in the normal plan.

## What was tested, and what was not

Tested, in `internal/tests/migration_test.go`, against an httptest mock gateway
and the real `terraform` CLI driven over committed legacy-shaped state fixtures:

- `TestMigrationFromLegacySameAddressPlansNothing` — for `asc_vpc`,
  `asc_subnet`, `asc_virtual_machine` and `asc_vm_group`, origin B state plus
  equivalent configuration using current attribute names plans zero changes, and the gateway was actually
  reached, so the empty plan is the result of a real refresh.
- `TestMigrationAssertionDetectsAReplacement` — the guard can fail. A gateway
  reporting a SKU the configuration does not ask for makes the plan replace the
  group, and the assertion reports it. Without this the four green cases above
  would be unfalsifiable.
- `TestMigrationFromReleasedProviderIsNotAnUpgrade` — origin A's state is never
  offered to this provider; the plan fails asking for `nl-ams-dspc/dspc`.
- `TestMigrationVanishedObjectPlansACreateNotADestroy` — an object deleted
  outside Terraform before the upgrade plans exactly one create and nothing
  else. A create is the right answer when the object is already gone; a delete
  here would mean the provider tried to destroy something the gateway says is
  absent.
- `TestMigrationWrongTenantFailsLoudly` — pointing the upgrade at the wrong
  namespace fails with an error and produces no planned change, so a refusal to
  read is never mistaken for an object having disappeared.
- `TestMigrationRetiredEntryFailsUntilRemoved` — a retired `asc_group_member`
  entry left in state stops the plan with an error naming the type, and
  `terraform state rm` makes the plan succeed. This is why the migration guide
  tells you to remove those entries.
- `TestMigrationImportAdoptsALiveMachine` — removes the legacy
  `dspc_virtual_machine` state entry, imports it as `asc_virtual_machine` by
  name, then requires an empty plan.
- `TestMigrationWithoutRefreshDetectsGroupReplacement` — a skipped refresh
  exposes the stale scaling attribute and the destructive-plan guard rejects it.
- `TestMigrationLegacyGroupConfigurationRequiresRename` — the old
  `autoscaling_policy` configuration argument fails before planning.
- Every request those tests send stays inside `/api/infra-gateway` and
  `/api/uam`; the suite fails on anything else, so a migration path cannot reach
  a retired service prefix.

Not tested, and stated here rather than left to be discovered:

- **Anything against a real ASC deployment.** Every test above talks to a mock.
- **Live apply, drift, cancellation and cleanup.** The runbook tests apply an
  inspected no-op against the mock, using saved and automatic plans, then
  re-plan and assert no gateway mutations. They do not apply to a live stack.
- **The renamed resources' re-import.** `asc_volume` and
  `asc_volume_attachment` are not imported by any test. The
  `asc_virtual_machine` import is covered, against the mock, so treat it as
  evidence that the mechanism works and not that your objects will import.
- **`asc_role`, `asc_group`, `asc_volume` and `asc_volume_attachment` from
  legacy state.** No migration fixture exists for them.
- **An in-place restore on a real backend.** The runbook test pulls a fixture
  backup, validates its lineage/serial/resources, pushes it into a separate empty
  local workspace without `-force`, and proves a refreshed no-op plan. Restoring
  an existing or remote backend requires the real environment.
- **A registry install.** Only a local snapshot signed with a throwaway key and
  installed from an offline filesystem mirror has been proven. Nothing has been
  published, and the real signing key has never been used.


### Command verification boundary

All operational steps require an authorized workspace. These local rehearsals
use Terraform 1.5.7 and 1.16.3 with synthetic state against httptest; other operations remain
explicit real-stack work.

| Commands | Verification |
| --- | --- |
| `version`, mirror `init`, `providers`, `providers schema -json` | `scripts/install-check.sh` runs these on the host release archive, without a live API. |
| `state pull`, `state list`, `state show`, `state push` without force | `TestMigrationRunbookBackupAndRestore`, isolated local backend only. |
| `plan -out`, `show -json`, `plan -json` | All migration cases; successful plans are checked from saved plan JSON. |
| `plan -refresh-only`, `show -json refresh.tfplan` | Mock backup/restore rehearsal. |
| Saved-plan and automatic no-op `apply` | `TestMigrationRunbookNoOpApply`; gateway requests must remain GET-only. |
| Legacy VM `state rm` then `import`, retired-entry `state rm` | Mock migration tests. |
| Origin-A inventory/removal, volume/attachment re-import, real backend restore, rollout gates, deliberate drift, cancellation/recovery and cleanup | **Requires the disposable real stack. Not performed.** |

The backup filename and JSON inspection snippets operate only on captured local
state/plan files. Keep the selected filename, lineage and serial in the handoff;
never substitute the first match of a backup wildcard.
