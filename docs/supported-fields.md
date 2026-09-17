# Supported fields

The generated pages under `docs/resources/` and `docs/data-sources/` list every attribute of every
resource and data source. This page answers what they cannot: which attribute can be changed on a
live resource, which one destroys and recreates it, how each resource is imported, and which gateway
gaps those answers follow from.

The provider registers six resources and six data sources:

| Resources | Data sources |
| --- | --- |
| `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_volume_attachment` | `asc_skus`, `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume` |

There is no `asc_load_balancer`. Data sources are read-only throughout, so none of them appears in
the change tables below.

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

## Import

All six resources are importable.

| Resource | Import ID | Example |
| --- | --- | --- |
| `asc_vpc` | `name` | `terraform import asc_vpc.example production` |
| `asc_subnet` | `vpc-name:subnet-name` | `terraform import asc_subnet.example production:web` |
| `asc_virtual_machine` | `name` | `terraform import asc_virtual_machine.example web-01` |
| `asc_vm_group` | `name` | `terraform import asc_vm_group.example web-pool` |
| `asc_volume` | `name` | `terraform import asc_volume.example data-01` |
| `asc_volume_attachment` | `volume-name:vm-name` | `terraform import asc_volume_attachment.example data-01:web-01` |

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
| A duplicate volume name returns an untyped HTTP 500, not a typed 409 | An untyped 500 can equally mean the create partially succeeded, so volume creation reports the error verbatim instead of retrying. VPC, virtual machine and VM group creation do retry, because their name collisions are typed 409s; group names share the virtual machine reservation table. |
| `PatchVolume` omits the tag conflict check the VPC, subnet and VM patch handlers perform | Breaching the tag limit on a volume surfaces as an untyped HTTP 500 rather than a 409. |
| Attach and detach answer with an acknowledgement only | Completion is read from the volume's own `status` and `attachedToVM`. Deleting a volume that is still attached is refused and reported as is; detaching is not the provider's decision to make. |

Only HTTP 404 removes a resource from state or makes a delete idempotent. Authentication,
validation, transport and other server errors are propagated.
