# Supported fields

The generated pages under `docs/resources/` and `docs/data-sources/` list every attribute of every
resource and data source. This page answers what they cannot: which attribute can be changed on a
live resource, which one destroys and recreates it, and how each resource is imported.

The provider registers nine resources and nine data sources, in three areas:

| Area | Resources | Data sources |
| --- | --- | --- |
| Infrastructure | `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_volume_attachment` | `asc_skus`, `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume` |
| Roles and groups | `asc_role`, `asc_group` | `asc_role`, `asc_group` |
| IP addresses | `asc_ip` | `asc_ips` |

There is no `asc_load_balancer`, no `asc_group_member` and no `asc_group_role`. Data sources are
read-only throughout, so none of them appears in the change tables below.

## Change behaviour

"In place" means Terraform applies the change to the live resource. "Replaces" means the plan
destroys and recreates it.

### asc_vpc

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required | Replaces | Renaming requires a new VPC. |
| `cidr` | Required | Replaces | Every inline subnet must lie within it. |
| `subnets` | Required, at least one | Replaces when a configured field of an inline subnet changes | Inline subnets cannot be changed in place. Only `name`, `cidr` and `tags` of each element are compared; the computed nested fields are ignored so that an unrelated edit does not replace the VPC. |
| `tags` | Optional | In place | Tags can be changed without replacing the VPC. |

Read-only: `id`, `urn`, `status`, `last_error`, and the `id`, `urn`, `vpc_id`, `status` and
`last_error` of every element of `subnets`.

### asc_subnet

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required | Replaces | |
| `vpc_name` | Required | Replaces | A subnet is addressed by the VPC and subnet name pair. |
| `vpc_id` | Required, UUID v4 | Replaces | |
| `cidr` | Required | Replaces | |
| `type` | Optional, deprecated | Never | ASC no longer has subnet types; the value is kept in state and never sent. |
| `tags` | Optional | In place | Tags can be changed without replacing the subnet. |

Read-only: `id`, `urn`, `status`, `last_error`.

### asc_virtual_machine

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, 1-50 characters, lowercase letter first, then lowercase letters, digits or hyphens, alphanumeric last | Replaces | |
| `sku_id` | Required | Replaces | Changing a machine's SKU in place is not supported by this provider. |
| `vpc_id` | Required | Replaces | |
| `image` | Optional, Computed | Replaces | Omitted, it requests `vm-images/ubuntu:24.04`. Keeps its prior value when the configuration omits it, so an unrelated change does not look like a new image. |
| `enable_logging` | Optional, deprecated | Never | ASC no longer has a logging setting; the value is kept in state and never sent. |
| `customization` | Optional, sensitive | Replaces | Write-only; see [what import cannot recover](#what-import-cannot-recover). |
| `tags` | Optional | In place | Tags can be changed without replacing the machine. |

Read-only: `urn`, `sku`, `os`, `status`, `last_error`, `attached_volumes`.

### asc_vm_group

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, same pattern and length as a virtual machine name | Replaces | |
| `sku_id` | Required | Replaces | |
| `vpc_id` | Required, UUID v4 | Replaces | |
| `image` | Optional | Replaces | Configuration-only: the original image cannot be read back. An empty value requests `vm-images/ubuntu:24.04`. |
| `autoscaling` | Required, with `min_replicas` and `max_replicas` between 1 and 100 and `max_replicas` at least `min_replicas`; optional `cron` block with required `timezone`, `start`, `end` and `desired_replicas` between 0 and 100 | Replaces | Changing the scaling policy in place is not supported. |
| `customization` | Optional, sensitive | Replaces | Never served back. |
| `tags` | Optional | Replaces | Changing group tags in place is not supported. |

Read-only: `urn`, `sku`, `status`, `last_error`, `attached_volumes`.

Every configurable attribute of a VM group requires replacement when changed.

### asc_volume

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, 1-50 characters | Replaces | |
| `size` | Required | In place, growth only | Capacity can be increased repeatedly. A smaller value is rejected at plan time with the current and requested byte counts; shrinking requires replacing the volume. |
| `storage_class` | Required | Replaces | |
| `access_mode` | Required, `ReadWriteOnce`, `ReadWriteMany` or `ReadOnlyMany` | Replaces | |
| `tags` | Optional | In place | Tags can be changed without replacing the volume. |

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

An attachment is identified by its volume and virtual machine names. Its status and last error
reflect the volume attachment state. A volume that is gone, or that is now attached to another machine, removes the
resource from state so the next plan recreates it.

### asc_role

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, 1-50 characters | Replaces | The name identifies the role. Renaming requires a new role. |
| `actions` | Required, set of 1 to 50 strings | In place | The granted actions can be changed without replacing the role. |

Read-only: `urn`.

The role is identified by its name within the configured tenant. Its granted actions form an
unordered set. Role IDs and descriptions are not exposed by this provider.

### asc_group

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `name` | Required, at least 3 characters | Replaces | Renaming requires a new group. |

Read-only: `id`, `urn`, `description`, `members`, and the `name`, `enabled` and `type` of every
element of `members`.

Members are sorted by name. Group descriptions and membership cannot be changed through this
provider; configuring either read-only field is rejected at plan time.

### asc_ip

ASC assigns an available IP address when this resource is created. There are no configurable
attributes; both attributes are read-only.

| Attribute | Schema | Change | Why |
| --- | --- | --- | --- |
| `ip` | Computed | Forces recreation if lost | The assigned IP address. Kept from state once known, because nothing about it can be recomputed from configuration. |
| `reserved_at` | Computed | Kept from state | Timestamp when the address was reserved. |

Read and import look up the reserved IP address. If it is no longer reserved, the resource is
removed from state and the next plan reserves a new address. A specific address cannot be requested.
Deleting the resource releases the address.

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

These values cannot be recovered from existing resources. They stay null after an import until
the configuration supplies them.

| Resource | Attributes | Consequence |
| --- | --- | --- |
| `asc_vm_group` | `vpc_id`, `image`, `customization` | They must come from the configuration after an import. Each forces replacement, so importing a group whose configuration differs plans a replacement rather than silently adopting it. |
| `asc_virtual_machine` | `customization` | Write-only. The value in state is the configured one, preserved across reads because it cannot be read back; an import leaves it null. |

## Read behavior and limitations

- Attached volume lists and the SKU catalog are sorted to keep their order stable across refreshes.
- Volume capacity may be reported using equivalent units, for example `10240Mi` for `10Gi`.
  Compare `size_bytes` when you need the capacity as a number. An equivalent spelling alone does
  not cause a resize.
- Provisioning and deletion can take time. The provider waits for completion or reports a timeout
  or resource error.
- Authentication failures, invalid role or group collections and server failures are reported as errors.
  They do not establish that a resource has been deleted. A valid empty role or group collection
  does establish absence, allowing Terraform to recreate a resource deleted outside Terraform.
- Detach a volume before deleting it. The provider reports a refused deletion rather than
  detaching it automatically.
- `asc_volume.attached_to_vm` reports the virtual machine's UUID, while
  `asc_volume_attachment.vm_name` takes its name.
- Load balancers, group membership changes and role assignments are not supported. See the
  [migration guide](guides/migration.md) for existing resources of these types.

## Role and group data sources

Both data sources take a `name` within the configured tenant and are read-only. `asc_role` reports
`urn` and `actions`; `asc_group` reports `id`, `urn`, `description` and `members`.

## IP address data source

`asc_ips` lists the tenant's reserved IP addresses in a stable order. `asc_ip` manages one reserved
address. Neither supports choosing a particular address at creation time.
