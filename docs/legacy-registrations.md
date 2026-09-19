# Legacy registration disposition

The legacy provider (`terraform-provider-dspc`) registered **22 resources and 19
data sources**, 41 registrations in total. Both counts match the card's
expectation exactly; the authoritative lists are the `Resources()` and
`DataSources()` slices in `internal/provider/provider.go` of that repository.

Every registration below is classified as exactly one of:

- **shipped** — already implemented in this provider against `/api/infra-gateway`.
- **uam** — implementable on `/api/uam` within this card, under the constraints
  in [the UAM contract](uam-contract.md).
- **deferred** — a real capability with no route on either approved boundary
  today. It returns only after API work on infra-gateway (or a new gateway
  surface); it is not reachable by any provider change alone.
- **retired** — no equivalent and no plan. Users migrate away from it.

The legacy provider reached eleven service prefixes directly
(`internal/client/config.go`). This provider reaches two: `/api/infra-gateway`
and `/api/uam`. No row below proposes calling a legacy prefix; where the legacy
transport column names one, that is the evidence for why the registration cannot
return as written, not a route the provider may use.

`/api/infra-gateway` serves exactly `/vms`, `/vm-groups`, `/vpcs` (including
`/vpcs/{vpc}/subnets` and NAT rules), `/volumes`, `/load-balancers`, `/images`,
`/skus` and `/v1/backup/resources/...`. Nothing else is registered on that
router, so any capability outside that list is deferred by construction.

## Resources

| Legacy address | Kind | Legacy transport | Disposition | Replacement | Evidence |
| --- | --- | --- | --- | --- | --- |
| `asc_vpc` | resource | `/api/network/v1/namespaces/{ns}/vpcs` | shipped | `asc_vpc` | `network.go:66-78`; gateway `/vpcs` in `vpcs.go registerRoutes`; `internal/resources/vpc/resource.go` |
| `asc_subnet` | resource | `/api/network/v1/namespaces/{ns}/vpcs/{vpc}/subnets` | shipped | `asc_subnet` | `network.go:83-112`; gateway `/vpcs/{vpcName}/subnets`; `internal/resources/subnet/resource.go` |
| `asc_virtual_machine` | resource | `/api/vm/v1/namespaces/{ns}/vms` | shipped | `asc_virtual_machine` | `virtual_machine.go:85-90`; gateway `/vms`; `internal/resources/virtualmachine/resource.go` |
| `asc_vm_group` | resource | `/api/vm/v1/namespaces/{ns}/vm-groups` | shipped | `asc_vm_group` | `vmgroup.go:62-67`; gateway `/vm-groups`; `internal/resources/vmgroup/resource.go` |
| `asc_block_storage` | resource | `/api/vm/v1/namespaces/{ns}/blocks` | shipped, renamed | `asc_volume` | `block_storage.go:130-142`; gateway `/volumes` in `blocks.go registerRoutes`; `internal/resources/volume/resource.go` |
| `asc_block_storage_attachment` | resource | `/api/vm/v1/namespaces/{ns}/blocks/{block}/attach/{vm}` | shipped, renamed | `asc_volume_attachment` | `block_storage.go:77-112`; gateway `POST`/`DELETE /volumes/{volumeName}/attach/{vmName}`; `internal/resources/volumeattachment/resource.go` |
| `asc_role` | resource | `/api/authorization/v1/roles/{name}` | uam | `asc_role` on `/api/uam/v1/tenants/{tenantId}/roles` | `authorization.go:44-58`; [UAM contract](uam-contract.md), "Routes the provider is allowed to use" |
| `asc_group` | resource | `/api/authorization/v1/groups/{name}` | uam | `asc_group` on `/api/uam/v1/tenants/groups` | `authorization.go:91-100`; [UAM contract](uam-contract.md), "Group identity, and what a group cannot express" |
| `asc_group_member` | resource | `/api/authorization/v1/groups/{group}/members/{user}` | retired | none | `authorization.go:105-110`; UAM exposes no member route — [UAM contract](uam-contract.md), "Capabilities UAM does not expose" |
| `asc_group_role` | resource | `/api/authorization/v1/groups/{group}/roles/{role}` | retired | none | `authorization.go:115-126`; UAM exposes no role-assignment route — same section |
| `asc_security_group` | resource | `/api/network/v1/namespaces/{ns}/security-groups` | deferred | none yet | `security_group.go:92-104`; infra-gateway registers no `/security-groups` route |
| `asc_security_rule` | resource | `/api/network/v1/namespaces/{ns}/security-groups/{sg}/rules` | deferred | none yet | `security_group.go:109-121`; no gateway route |
| `asc_security_group_attachment` | resource | `/api/network/v1/namespaces/{ns}/security-groups/{sg}/attach` | deferred | none yet | `security_group.go:140-163`; no gateway route |
| `asc_load_balancer` | resource | `/api/network/v1/namespaces/{ns}/load-balancers` | deferred | none yet | `loadbalancer.go:63`; `loadbalancers.go registerRoutes` has `POST`, `PATCH`, `DELETE` and **no** `GET` or list, so a resource could not refresh, detect drift or import |
| `asc_mssql` | resource | `/api/mdb/v1/databases/{name}` | deferred | none yet | `mssql.go:65-83`; no managed-database route on infra-gateway |
| `asc_postgresql` | resource | `/api/mdb/v1/databases/{name}` | deferred | none yet | `postgresql.go:59-77`; no managed-database route on infra-gateway |
| `asc_file_storage` | resource | `/api/file/v1/file-storages/{name}` | deferred | none yet | `file_storage.go:56-68`; no gateway route |
| `asc_file_storage_access` | resource | `/api/file/v1/file-storages/{name}/access/{type}/{target}` | deferred | none yet | `file_storage.go:74-92`; no gateway route |
| `asc_object_storage` | resource | `/api/object-storage/v1/object-storage/{id}` | deferred | none yet | `object_storage.go:63-102`; no gateway route |
| `asc_function` | resource | `/api/serverless-container/v1/functions/{name}` | deferred | none yet | `function.go:110-126`; no gateway route |
| `asc_container` | resource | `/api/containers/v1/deployments/{name}` | deferred | none yet | `container.go:70-78`; no gateway route |
| `asc_cluster` | resource | `/api/cluster/v1/clusters/{name}` | deferred | none yet | `cluster.go:154-181`; no gateway route |

## Data sources

| Legacy address | Kind | Legacy transport | Disposition | Replacement | Evidence |
| --- | --- | --- | --- | --- | --- |
| `asc_vpcs` | data source | `/api/network/v1/namespaces/{ns}/vpcs` | shipped, reshaped | `asc_vpc` (single, by name) | `vpc_data_source.go:64` returns a `vpcs` list; `internal/datasources/vpc/data_source.go` is singular |
| `asc_subnets` | data source | `/api/network/v1/namespaces/{ns}/vpcs/{vpc}/subnets` | shipped, reshaped | `asc_subnet` (single, by VPC and name) | `subnet_data_source.go:71`; `internal/datasources/subnet/data_source.go` |
| `asc_virtual_machines` | data source | `/api/vm/v1/namespaces/{ns}/vms` | shipped, reshaped | `asc_virtual_machine` (single, by name) | `virtual_machine_data_source.go:66`; `internal/datasources/virtualmachine/data_source.go` |
| `asc_vm_groups` | data source | `/api/vm/v1/namespaces/{ns}/vm-groups` | shipped, reshaped | `asc_vm_group` (single, by name) | `vmgroup_data_source.go:72`; `internal/datasources/vmgroup/data_source.go` |
| `asc_skus` | data source | `/api/vm/v1/namespaces/{ns}/skus` | shipped | `asc_skus` | `sku.go` `ListSKUs`; gateway `GET /skus`; `internal/datasources/sku/data_source.go` |
| `asc_block_storage` | data source | `/api/vm/v1/namespaces/{ns}/blocks/{name}` | shipped, renamed | `asc_volume` | `block_storage.go:136`; gateway `GET /volumes/{volumeName}`; `internal/datasources/volume/data_source.go` |
| `asc_block_storage_attachment` | data source | `/api/vm/v1/namespaces/{ns}/virtualmachines/{vm}/blocks` | retired | `asc_volume`'s `attached_to_vm` and `status` | `block_storage.go:93`; infra-gateway has no attachment read route, and the volume read model already reports the attachment — `internal/datasources/volume/data_source.go` |
| `asc_role` | data source | `/api/authorization/v1/roles/{name}` | uam | `asc_role` data source on `/api/uam/v1/tenants/{tenantId}/roles` | `authorization.go:58`; [UAM contract](uam-contract.md) list and get role routes |
| `asc_group` | data source | `/api/authorization/v1/groups/{name}` | uam | `asc_group` data source on `/api/uam/v1/tenants/groups` | `authorization.go:91`; [UAM contract](uam-contract.md) list and get group routes |
| `asc_security_groups` | data source | `/api/network/v1/namespaces/{ns}/security-groups` | deferred | none yet | `security_group.go:92`; no gateway route |
| `asc_security_rules` | data source | `/api/network/v1/namespaces/{ns}/security-groups/{sg}/rules` | deferred | none yet | `security_group.go:115`; no gateway route |
| `asc_security_group_attachment` | data source | `/api/network/v1/namespaces/{ns}/security-groups/{sg}/attachments` | deferred | none yet | `security_group.go:150-157`; no gateway route |
| `asc_mssql` | data source | `/api/mdb/v1/databases/{name}` | deferred | none yet | `mssql.go:65`; no gateway route |
| `asc_postgresql` | data source | `/api/mdb/v1/databases/{name}` | deferred | none yet | `postgresql.go:59`; no gateway route |
| `asc_file_storage` | data source | `/api/file/v1/file-storages/{name}` | deferred | none yet | `file_storage.go:56`; no gateway route |
| `asc_file_storage_access` | data source | `/api/file/v1/file-storages/{name}/access/{type}/{target}` | deferred | none yet | `file_storage.go:86`; no gateway route |
| `asc_object_storage` | data source | `/api/object-storage/v1/object-storage/{id}` | deferred | none yet | `object_storage.go:63`; no gateway route |
| `asc_function` | data source | `/api/serverless-container/v1/functions/{name}` | deferred | none yet | `function.go:126`; no gateway route |
| `asc_container` | data source | `/api/containers/v1/deployments/{name}` | deferred | none yet | `container.go:70`; no gateway route |

## Totals by disposition

| Disposition | Resources | Data sources | Total |
| --- | --- | --- | --- |
| shipped | 6 | 6 | 12 |
| uam | 2 | 2 | 4 |
| deferred | 12 | 10 | 22 |
| retired | 2 | 1 | 3 |
| **total** | **22** | **19** | **41** |

Two legacy registrations had no data-source counterpart (`asc_cluster`,
`asc_load_balancer`), and no legacy data source lacked a resource, which is why
the resource and data-source columns do not line up row for row.

## Migrating each non-shipped registration

### `asc_block_storage` and `asc_block_storage_attachment` (renamed, not removed)

Shipped, but under new addresses. The state entries cannot be reused, because
the provider's internal schema and type names differ. Remove the old entries and
import the live objects:

```
terraform state rm asc_block_storage.data
terraform state rm asc_block_storage_attachment.data
terraform import asc_volume.data data-01
terraform import asc_volume_attachment.data data-01:web-01
```

Import IDs are in [supported fields](supported-fields.md#import).

### The plural data sources (`asc_vpcs`, `asc_subnets`, `asc_virtual_machines`, `asc_vm_groups`)

Data sources hold no durable state, so nothing has to be removed by hand; a
`terraform plan` after the address change refreshes them. The shape changes:
each legacy data source returned a list attribute (`vpcs`, `subnets`, and so
on), and each replacement resolves a single object by name. Configurations that
indexed into the list must select by name instead. `asc_skus` keeps its name and
its list shape.

### `asc_block_storage_attachment` (data source) — retired

There is no standalone attachment read on infra-gateway. Delete the data-source
block and read the same facts from `asc_volume`: `attached_to_vm` names the VM
and `status` reports whether the attachment has converged.

### `asc_role` and `asc_group` — UAM

Both come back on `/api/uam`, with the identity and update limits set out in
[the UAM contract](uam-contract.md). Two consequences for a migration:

- A role's Terraform identity is its **name**, not the legacy `id`. State cannot
  be moved from the legacy entry, because the legacy entry persisted an id whose
  meaning differed per route. `terraform state rm` the old entry and import by
  name. Renaming a role is not expressible on UAM, so `name` forces replacement
  and only `actions` updates in place.
- A group has no update route at all: every configurable attribute forces
  replacement, and `description` and `members` are read-only. A configuration
  that set them on the legacy resource must drop those arguments before the
  first plan. Both are `Computed` with no `Optional`, so Terraform rejects a
  configuration that sets either with a schema error rather than planning a
  change UAM would then refuse.

### `asc_group_member` and `asc_group_role` — retired, no equivalent

These are the two registrations with no path forward of any kind. UAM exposes no
route that adds or removes a group member, and no route that assigns a role to a
user, a group or an API client
([UAM contract](uam-contract.md), "Capabilities UAM does not expose"). The
capability is therefore not deferred behind a provider change; it does not exist
on the approved boundary.

A user with either in state must:

1. `terraform state rm asc_group_member.<name>` and
   `terraform state rm asc_group_role.<name>` for every such entry. There is
   nothing to import them into, and leaving them in state makes every subsequent
   plan fail once the legacy provider is removed.
2. Delete the corresponding configuration blocks.
3. Administer memberships and role assignments manually through the UAM console
   or the UAM API until UAM ships routes for them.

Removing the entries from state does not touch the live memberships or
assignments — `state rm` forgets, it does not delete — so existing access keeps
working; it simply stops being managed by Terraform.

### The deferred registrations

`asc_security_group`, `asc_security_rule`, `asc_security_group_attachment`,
`asc_load_balancer`, `asc_mssql`, `asc_postgresql`, `asc_file_storage`,
`asc_file_storage_access`, `asc_object_storage`, `asc_function`,
`asc_container`, `asc_cluster` and their data-source counterparts are real
capabilities with live services behind them. What is missing is a route on an
approved boundary, not an implementation in this provider, so none of them can
be brought back by provider work alone.

For each one, the migration today is the same: `terraform state rm` the entry,
delete the configuration block, and manage the object outside Terraform until
the capability is exposed on infra-gateway. The underlying objects are not
destroyed by removing them from state.

`asc_load_balancer` is the closest of these to returning and is worth calling
out separately: infra-gateway already serves `POST`, `PATCH` and `DELETE
/load-balancers`, and only the read is missing. A resource without a read cannot
refresh, detect drift or import, which is why it is absent rather than shipped
write-only. A single `GET /load-balancers/{name}` on the gateway would move this
row from deferred to shippable; nothing else about it is blocked.

## Evidence

- Legacy provider at `terraform-provider-dspc` `1c460cc` ("Merge pull request
  #48 from NL-AMS-DSPC/feature/ASCP-2013"):
  `internal/provider/provider.go` (the `Resources()` and `DataSources()`
  slices), `internal/client/config.go` (the eleven service path prefixes),
  `internal/client/client.go` (`namespacedPath`, which prepends
  `/v1/namespaces/{namespace}`), and
  `internal/client/{authorization,block_storage,cluster,container,file_storage,function,loadbalancer,mssql,postgresql,network,object_storage,security_group,sku,virtual_machine,vmgroup}.go`
  for the per-registration paths. Legacy Terraform addresses were read from the
  `resp.TypeName` assignments under `internal/resources/**`.
- ASC at `72674111` ("Release a VM group's name when its deletion is
  acknowledged"):
  `services/infra-gateway/adapters/http/handler.go` for the set of route groups
  mounted on the router, and
  `services/infra-gateway/adapters/http/{vms,vmgroups,vpcs,blocks,loadbalancers,images,skus,backups}.go`
  for each group's `registerRoutes`.
- This provider at `4f7096f`: `internal/provider/provider.go`,
  `internal/resources/**` and `internal/datasources/**` for what ships, plus
  [`docs/uam-contract.md`](uam-contract.md),
  [`docs/infra-gateway-core-workflow.md`](infra-gateway-core-workflow.md) and
  [`docs/supported-fields.md`](supported-fields.md).

Line numbers in the tables point at the legacy files above and are the exact
call sites that name each path. Where a row's evidence says "no gateway route",
that is read from the absence of a matching `registerRoutes` entry in
infra-gateway, not from a negative statement in any source.
