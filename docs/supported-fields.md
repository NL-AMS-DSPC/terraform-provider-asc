# Supported fields

The generated pages under `docs/resources/` and `docs/data-sources/` list every attribute of every
resource and data source. This page answers what they cannot: which attribute can be changed on a
live resource, which one destroys and recreates it, how each resource is imported, and which gateway
gaps those answers follow from.

The provider registers nine resources and nine data sources, across three API boundaries:

| Boundary | Resources | Data sources |
| --- | --- | --- |
| `/api/infra-gateway` | `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_volume_attachment` | `asc_skus`, `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume` |
| `/api/uam` | `asc_role`, `asc_group` | `asc_role`, `asc_group` |
| `/api/ipam` | `asc_ip` | `asc_ips` |

There is no `asc_load_balancer`, no `asc_group_member` and no `asc_group_role`. Data sources are
read-only throughout, so none of them appears in the change tables below; the UAM data sources have
their own section, because what a UAM read can report is narrower than what its resource holds.

## Change behaviour

"In place" means Terraform applies the change to the live resource. "Replaces" means the plan
destroys and recreates it. Where the reason is a gateway limitation rather than a Terraform one, it
is stated.

### asc_vpc

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required | Replaces | The name identifies the VPC to the gateway. |
| `subnets` | Required, at least one | Replaces when a configured field of an inline subnet changes | No route changes an inline subnet in place. Only `name`, `cidr`, `type` and `tags` of each element are compared; the computed nested fields are ignored so that an unrelated edit does not replace the VPC. |
| `tags` | Optional | In place | `PATCH /vpcs/{name}`. |

Read-only: `id`, `urn`, `cidr`, `status`, `last_error`, and the `id`, `urn`, `vpc_id`, `status` and
`last_error` of every element of `subnets`.

### asc_subnet

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required | Replaces | |
| `vpc_name` | Required | Replaces | A subnet is addressed by the VPC and subnet name pair. |
| `vpc_id` | Required, UUID v4 | Replaces | |
| `cidr` | Required | Replaces | |
| `type` | Required, `public` or `private` | Replaces | |
| `tags` | Optional | In place | `PATCH /vpcs/{vpc}/subnets/{name}`. |

Read-only: `id`, `urn`, `status`, `last_error`.

### asc_virtual_machine

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, 1-50 characters, lowercase letter first, then lowercase letters, digits or hyphens, alphanumeric last | Replaces | |
| `sku_id` | Required | Replaces | A resize route exists but `request_vm_resizing` latches `isResizingRequested` and never clears it, so a machine accepts exactly one resize for its whole lifetime; an in-place update would work once and then fail permanently. |
| `vpc_id` | Required | Replaces | |
| `image` | Optional, Computed | Replaces | Keeps its prior value when the configuration omits it, so an unrelated change does not look like a new image. |
| `enable_logging` | Optional, Computed | Replaces | Same prior-value handling as `image`. |
| `customization` | Optional, sensitive | Replaces | Write-only; see [what import cannot recover](#what-import-cannot-recover). |
| `tags` | Optional | In place | `PATCH /vms/{name}`. |

Read-only: `urn`, `sku`, `os`, `status`, `last_error`, `attached_volumes`.

### asc_vm_group

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, same pattern and length as a virtual machine name | Replaces | |
| `sku_id` | Required | Replaces | |
| `vpc_id` | Required, UUID v4 | Replaces | |
| `image` | Optional | Replaces | Configuration-only: the gateway never echoes it back, so it cannot be computed. An empty value defaults server-side. |
| `autoscaling` | Required, with `min_replicas` and `max_replicas` between 1 and 100 and `max_replicas` at least `min_replicas`; optional `cron` block with required `timezone`, `start`, `end` and `desired_replicas` between 0 and 100 | Replaces | infra-gateway registers no scale or update route for a group's policy. |
| `customization` | Optional, sensitive | Replaces | Never served back. |
| `tags` | Optional | Replaces | infra-gateway registers no PATCH route for a group, so tags cannot be reconciled in place the way a VPC's can. |

Read-only: `urn`, `sku`, `status`, `last_error`, `attached_volumes`.

The resource's `Update` is a no-op: every configurable attribute forces replacement, so Terraform
never reaches an in-place update.

### asc_volume

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, 1-50 characters | Replaces | |
| `size` | Required | In place, growth only | `POST /volumes/{name}/resize`. The resize slice clears its latch on both the acknowledged and the failed event, so unlike a virtual machine resize this is repeatable. A smaller value is rejected at plan time with the current and requested byte counts; shrinking requires replacing the volume. |
| `storage_class` | Required | Replaces | |
| `access_mode` | Required, `ReadWriteOnce`, `ReadWriteMany` or `ReadOnlyMany` | Replaces | |
| `tags` | Optional | In place | `PATCH /volumes/{name}`. |

Read-only: `size_bytes`, `volume_mode`, `urn`, `status`, `last_error`, `attached_to_vm`, `labels`,
`annotations`.

A plan that changes anything on a volume marks `size_bytes`, `status`, `last_error`,
`attached_to_vm`, `labels` and `annotations` unknown, because both the resize and the tag patch
settle asynchronously and state is taken from an authoritative re-read afterwards.

### asc_volume_attachment

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `volume_name` | Required | Replaces | |
| `vm_name` | Required | Replaces | Moving a volume is a detach followed by an attach, not an update. |

Read-only: `status`, `last_error`.

infra-gateway has no attachment object of its own; the relationship lives on the volume, so the pair
of names is the whole identity and the two computed attributes are the volume state that proves the
attachment landed. A volume that is gone, or that is now attached to another machine, removes the
resource from state so the next plan recreates it.

### asc_role

Roles live on `/api/uam`, not on infra-gateway, and the differences below are all forced by the
[UAM contract](uam-contract.md).

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, 1-50 characters | Replaces | The name is the role's whole Terraform identity. UAM's update addresses the role by the name in the request body and ignores the path segment, so a renamed role would be written nowhere. |
| `actions` | Required, set of 1 to 50 strings | In place | `PUT /v1/tenants/{tenant}/roles/{name}`. |

Read-only: `urn`.

Where the values come from:

- `name` is the configured value. The list route proves it exists; the provider never adopts a name
  from a response.
- `actions` are sent as `actions` on create and update, and read back from the single-item GET as
  `composites`. The list route serves no composites at all, so actions cannot be refreshed from it.
  The set is sorted provider-side because the order is not preserved.
- `urn` comes from the single-item GET.

There is no `id` attribute, on purpose. `RoleResponse` carries one, but the single-item GET sets it
to the role's name while the list route sets it to a database uuid, so the two routes disagree about
what it holds and no single value could be kept in state. The uuid is still required in an update
body, and UAM's upsert conflicts on it rather than on the name, so the provider resolves it from the
list route on every update; a request carrying an invented id inserts a duplicate row for the same
role name.

There is no `description` attribute either: no route accepts one on input, and the single-item read
reports it empty.

### asc_group

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, at least 3 characters | Replaces | UAM registers no update route for a group, and create accepts `groupName` and nothing else. |

Read-only: `id`, `urn`, `description`, `members`, and the `name`, `enabled` and `type` of every
element of `members`.

Where the values come from:

- `name` is the configured value and is how every route addresses the group, the delete included.
- `id` and `description` come from the list route, which serves database rows. The id is stable
  across reads, but it is not the addressing key.
- `urn` and `members` come from the single-item GET, the only route that carries members, despite
  the list route's doc comment. Members are sorted by name provider-side, because they are assembled
  from a user lookup followed by an API client lookup and arrive in the upstream directories' order.
- `description` and `members` are read-only because no route sets a description and no route adds or
  removes a member. A configuration that sets either is rejected at plan time.

The resource's `Update` is a no-op: the only configurable attribute forces replacement.

A duplicate group name is reported by UAM as a plain error rather than its already-exists sentinel,
so it arrives as HTTP 502 and is indistinguishable from a genuine upstream failure. The provider
does not retry it; it consults the list route once afterwards so the diagnostic can name the taken
name rather than repeat the status.

### asc_ip

Reservations live on `/api/ipam`. There is no configurable attribute at all: `POST /reservations`
takes no body and IPAM picks the address, so every attribute is read-only.

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `ip` | Computed | Forces recreation if lost | The address IPAM assigned. Kept from state once known, because nothing about it can be recomputed from configuration. |
| `reserved_at` | Computed | Kept from state | Timestamp `POST /reservations` returns alongside the address. |

`Read` and import both filter `GET /reservations` for the
matching `ip`. A reservation missing from that list removes the resource from state, and the next
plan reserves a new, different address; there is no way to ask IPAM for a specific one back.

The resource's `Update` is unreachable: both attributes are computed, so no configuration change can
produce a plan that reaches it. Deleting it sends `DELETE /reservations` with `{"ip": "..."}` in the
body.

## Import

All nine resources are importable.

| Resource | Import ID | Example |
| --- | --- | --- |
| `asc_vpc` | `name` | `terraform import asc_vpc.example production` |
| `asc_subnet` | `vpc-name:subnet-name` | `terraform import asc_subnet.example production:web` |
| `asc_virtual_machine` | `name` | `terraform import asc_virtual_machine.example web-01` |
| `asc_vm_group` | `name` | `terraform import asc_vm_group.example web-pool` |
| `asc_volume` | `name` | `terraform import asc_volume.example data-01` |
| `asc_volume_attachment` | `volume-name:vm-name` | `terraform import asc_volume_attachment.example data-01:web-01` |
| `asc_role` | `name` | `terraform import asc_role.example network-admin` |
| `asc_group` | `name` | `terraform import asc_group.example platform-team` |
| `asc_ip` | `ip` | `terraform import asc_ip.example 10.0.0.1` |

The two pair-formed identifiers split on the first `:`; an identifier with no separator, or with
nothing on either side of it, is rejected with a diagnostic naming the expected form.

### What import cannot recover

infra-gateway does not serve these values back, so a read cannot fill them and the provider does not
invent them. They stay null after an import until the configuration supplies them.

| Resource | Attributes | Consequence |
| --- | --- | --- |
| `asc_vm_group` | `vpc_id`, `image`, `customization` | They must come from the configuration after an import. Each forces replacement, so importing a group whose configuration differs plans a replacement rather than silently adopting it. |
| `asc_virtual_machine` | `customization` | Write-only. The value in state is the configured one, preserved across reads because the GET deliberately omits it; an import leaves it null. |

## What the gateway does not serve

| Gap | Consequence for the provider |
| --- | --- |
| No load-balancer read route | There is no `asc_load_balancer` at all, by decision: a resource that cannot refresh could not detect drift or import. |
| No single-subnet GET | The subnet read lists `GET /vpcs/{vpc}/subnets/` and matches on the exact name; a miss is reported as not found. |
| The VM read model builds `attachedVolumes` by ranging a Go map | The order differs between two reads of an unchanged machine, so the client sorts the list before it reaches state. A VM group's attached volumes are sorted for the same reason. |
| No collection is served in a guaranteed order | Every list recorded positionally in state is sorted provider-side on the field that identifies its element, `asc_skus` by SKU id included, so that a reshuffle alone never shows up as a diff. |
| `size` is an opaque Kubernetes quantity | The gateway may echo an equal size back in a different spelling, for example `10240Mi` for `10Gi`. The provider keeps the configured spelling while the byte count matches and adopts the gateway's only when the magnitude really changed, which is what an out-of-band resize looks like. `size_bytes` is the value to compare against. |
| Virtual machines and virtual machine groups share one name reservation | `vm_names_in_use` is keyed on the tenant and name for both, so a group cannot take a machine's name or the reverse. infra-gateway releases a group's name when it acknowledges the group's deletion, which is what lets a replacement reuse it. A 409 from `POST /vm-groups` therefore always means the name is taken right now, and the provider reports it at once rather than retrying. A group's replica names, `<group>-0` and so on, are not released on deletion; that is a separate gateway gap and does not affect the group's own name. |
| A duplicate volume name returns an untyped HTTP 500, not a typed 409 | An untyped 500 can equally mean the create partially succeeded, so volume creation reports the error verbatim instead of retrying. VPC, virtual machine and VM group creation do retry, because their name collisions are typed 409s; group names share the virtual machine reservation table. |
| `PatchVolume` omits the tag conflict check the VPC, subnet and VM patch handlers perform | Breaching the tag limit on a volume surfaces as an untyped HTTP 500 rather than a 409. |
| Attach and detach answer with an acknowledgement only | Completion is read from the volume's own `status` and `attachedToVM`. Deleting a volume that is still attached is refused and reported as is; detaching is not the provider's decision to make. |
| `attachedToVM` holds the machine's instance id, not its name | `get_volume` records `VMAttachedToVolumeAcknowledgedEvent.InstanceID` and drops the `VMName` the same event carries, so the field named after a machine name holds a uuid. The provider resolves the machine through `GET /vms/{name}` and matches its urn's resource id, accepting either spelling so a gateway that starts serving the name keeps working. `asc_volume.attached_to_vm` and the `asc_volume` data source therefore expose the uuid; `asc_volume_attachment.vm_name` stays the configured name. |

On infra-gateway, only HTTP 404 removes a resource from state or makes a delete idempotent.
Authentication, validation, transport and other server errors are propagated.

## The UAM data sources

Both UAM data sources take a `name` and are read-only. `asc_role` reports `urn` and `actions`;
`asc_group` reports `id`, `urn`, `description` and `members`. Each value comes from the same route
the resource reads it from, so a data source is subject to the same split: a role's actions and a
group's members exist only on the single-item GET, while a group's id and description exist only on
the list route.

Neither data source exposes a role's UAM `id`, for the reason given under
[`asc_role`](#asc_role).

## What UAM does not serve

| Gap | Consequence for the provider |
| --- | --- |
| A missing role or group answers HTTP 502, not 404 | UAM turns every non-2xx from the authorization service into an untyped error and maps only its own not-found sentinels to 404. Existence is therefore decided from a list route and never from a single-item GET; absence from the list is what removes the resource from state. A 502 from a single-item GET is reported as an error, never treated as "gone". |
| A role's `id` means one thing on the single-item GET and another on the list | The Terraform identity is the name. See [`asc_role`](#asc_role). |
| No group update route | Every configurable attribute of `asc_group` forces replacement. |
| No route sets a group's description, and none adds or removes a member | `description` and `members` are computed. |
| No route assigns a role to a user, a group or an API client, and no route manages group membership | There is no `asc_group_member` and no `asc_group_role`. Both legacy registrations are retired rather than deferred: the capability is absent from the approved boundary, not merely unimplemented here. See the [migration guide](guides/migration.md). |
| Both delete routes spell their path parameter `{roleID}` and `{groupID}` | The services resolve either segment by name, so the segment sent is the name in both cases. |

The provider reaches exactly three API prefixes, `/api/infra-gateway`, `/api/uam` and `/api/ipam`,
plus the Keycloak realm token path used to obtain a bearer token. The acceptance suite asserts that
against an allowlist over every route the provider actually sends, so a request to any other prefix
fails the build.

## IPAM

`asc_ip` and `asc_ips` are the only resources on `/api/ipam`. Unlike
infra-gateway, IPAM responses are never wrapped in a `data` envelope, and unlike UAM there is no
tenant-scoped path segment to resolve first: `/reservations` is the entire route, for all three
verbs.
