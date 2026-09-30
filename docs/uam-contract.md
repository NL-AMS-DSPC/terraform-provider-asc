# Minimal UAM Terraform contract

The provider's second boundary is `/api/uam`. The gateway rewrites that prefix
away, so `/api/uam/v1/...` reaches the UAM service as `/v1/...`
(`applications/overlays/dev/uam-service/httproute.yaml`). Authentication is the
same client-credentials bearer token the infra-gateway boundary already uses,
and the configured namespace is sent as `X-Tenant`.

Two differences from infra-gateway are structural and neither is negotiable:

- **No `data` envelope.** infra-gateway answers through
  `httptransport.RespondWithJSON`, which wraps the payload in `{"data": ...}`.
  UAM answers through `http_api.RespondWithJSON` →
  `httptransport.RespondWithJSONObject`, which encodes the payload bare. The
  `callInfra` helper unwraps `data`; `callUAM` decodes bare JSON. Null bodies,
  missing group arrays and nameless collection entries are rejected before deciding absence.
- **Missing resources are not 404.** UAM's downstream `fromResponse` turns every
  non-2xx from the authorization service into an untyped
  `fmt.Errorf("request failed with status %d: ...")`, and `HandleServiceError`
  only maps the `domain.Err*NotFound` sentinels. A role or group that no longer
  exists therefore surfaces as **HTTP 502**, not 404. Existence must be decided
  from a list route, never from a single-item GET.

## Routes the provider is allowed to use

Every route below is authorized in
`dspc-cluster-gitops/applications/base/uam-service/authorization-routes.yaml`.

| Operation | Route | Status | Body |
| --- | --- | --- | --- |
| Get tenant | `GET /v1/tenants/{clientId}` | 200 | bare `TenantResponse` |
| List tenant roles | `GET /v1/tenants/{tenantId}/roles` | 200 | bare `[]RoleResponse` |
| Get tenant role | `GET /v1/tenants/{tenantId}/roles/{roleName}` | 200 | bare `RoleResponse` |
| Create role | `POST /v1/tenants/{tenantId}/roles` | 201 | `{"message": "..."}` |
| Update role | `PUT /v1/tenants/{tenantId}/roles/{segment}` | 200 | `{"message": "..."}` |
| Delete role | `DELETE /v1/tenants/{tenantId}/roles/{segment}` | 204 | empty |
| List groups | `GET /v1/tenants/groups` | 200 | bare `{"groups": [...]}` |
| Get group | `GET /v1/tenants/groups/{groupName}` | 200 | bare `GroupResponse` |
| Create group | `POST /v1/tenants/groups` | 201 | bare `GroupResponse` |
| Delete group | `DELETE /v1/tenants/groups/{segment}` | 200, empty body | none |

The authorization routes are written without a trailing slash, and ext-authz
matches the request path against those patterns before the request reaches chi.
The provider therefore sends the collection routes without a trailing slash even
though chi would accept either form.

Groups are registered as a static `/groups` child of the `/tenants` router, so
their path carries no tenant segment; the tenant comes from `X-Tenant` only.
Roles carry the tenant in both the path and the header, and both values must be
the same identifier.

## Tenant identifier

`X-Tenant` reaches the service as the security context's `TenantID`
(`securitycontext.TenantHeader`), and the role and group services resolve it
with `getTenantIDByClientID`, which looks the tenant up **by client ID**. The
provider's `namespace` is therefore the tenant's client ID, the same value the
infra-gateway boundary already sends.

**The role item read is the exception, and the two identifiers cannot both be
right.** `CreateRole`, `UpdateRole` and `DeleteRole` resolve their `{tenantId}`
segment through `getTenantIDByClientID` and call the authorization service with
`tenant.ID`, the tenant's Keycloak client UUID (`adapters/keycloak/client.go`
returns it from `CreateClient` and the tenants table stores it as the row id).
`GetTenantRole` does not: it hands the path segment to `GetClientRole`, which
sends it verbatim as the downstream tenant header, and the authorization
service keys a role in SpiceDB as `<tenant>_<name>`
(`adapters/spicedb/id_helpers.go`). A role created under the namespace is
therefore stored as `<uuid>_<name>`, and reading it back under the namespace
looks up `<clientId>_<name>`, which was never written and answers **502**.
Addressing the writes by the UUID instead is not an option either: their
`getTenantIDByClientID` finds no tenant whose client ID is a UUID and fails.

The provider resolves the split rather than choosing one identifier: it reads
the tenant's UUID once from `GET /v1/tenants/{clientId}` and uses it only as the
segment of the role item read, keeping the namespace on the list, create, update
and delete routes. Two consequences follow. The provider's credentials need the
`uam:GetTenant` action as well as the role and group actions. And this mirrors a
UAM defect rather than its contract: if `GetTenantRole` is ever changed to
translate its segment, `Runtime.roleReadPath` must go back to the namespace at
the same time, because the fixed service would reject a UUID segment.

Groups are unaffected: every group route resolves the tenant from `X-Tenant`
through the same translation, and their path carries no tenant segment at all.

The gateway forwards `X-Tenant` to ext-authz for authorization
(`securitypolicy.yaml`, `headersToExtAuth`) and does not rewrite it, so a
client-supplied tenant is authorized rather than trusted.

## Role identity is the name, and the `id` field is not one identity

`RoleResponse` carries an `id`, but three routes disagree about what it holds:

- `GET .../roles/{roleName}` comes from the authorization service and sets
  `ID = Name` with `Composites` holding the role's **actions**.
- `GET .../roles/` comes from UAM's own database and sets `ID` to the row's
  UUID, with `Description` populated and **no** composites/actions at all.
- `POST .../roles/` assigns a fresh server-side UUID that it never returns; the
  response body is only a message.

Consequences the resource implementation must obey:

1. The Terraform identity of `asc_role` is its **name**. The provider must not
   persist UAM's `id`, because the value depends on which route produced it.
2. `actions` on create map to `composites` on the single-item read. The list
   route cannot be used to refresh actions.
3. `description` is accepted nowhere on input and is empty on the single-item
   read, so it is not a supported attribute.
4. `PUT` requires `id` in the body (`UpdateRoleRequest.ID`, `validate:"required"`)
   and the handler ignores its path segment entirely — the downstream client
   addresses the role by `roleRequest.Name`. The provider sends the role's name
   in that segment anyway, so the request reads as what it does even though UAM
   discards it. The DB upsert conflicts on the id
   column, not on the name, so a `PUT` carrying an invented id **inserts a
   duplicate row** for the same role name. An update must therefore read the
   list route first, take the UUID recorded for that name, and send it.
5. Renaming is not expressible: the downstream `PUT` addresses the role by the
   name in the body, so a new name creates nothing and updates nothing. `name`
   requires replacement; only `actions` updates in place.
6. `DELETE .../roles/{segment}` names its chi parameter `roleID`, but
   `RoleService.DeleteRole` resolves the segment with `repo.GetByName` and
   forwards the same segment to the authorization service. The segment is the
   role **name**.

## Group identity, and what a group cannot express

- `POST /v1/tenants/groups` accepts only `{"groupName": "..."}` (minimum length
  3). `description` and `members` exist on `GroupResponse` but are not settable.
- The create response's `id` is a UUID the handler mints with `uuid.New()` and
  persists, so it is stable and matches later reads — but it is still not the
  addressing key, and the provider does not read it. Create is followed by an
  authoritative read, so the `id` in Terraform state always comes from the list
  route.
- `DELETE /v1/tenants/groups/{segment}` names its parameter `groupID`, while
  `GroupService.DeleteGroup` resolves it with `GetGroupByNameAndTenant`. As with
  roles, the segment is the **name**.
- There is no update route. Every configurable attribute requires replacement.
- Creating a group whose name already exists fails with a plain
  `fmt.Errorf("group %s already exists")`, not `domain.ErrGroupAlreadyExists`,
  so it is reported as **502**, not 409. The provider cannot distinguish that
  case from a genuine upstream failure and must not retry it.
- `members` is read-only and is assembled from users and API clients. There is
  no route to add or remove a member, so group membership is not manageable.
- The list route returns database rows only (`repository.List`), so it carries
  `id`, `name` and `description` but never members, despite its doc comment.
  Members come from the single-item GET alone, exactly as a role's actions do.

## Capabilities UAM does not expose

No route assigns a role to a user, a group or an API client, and no route adds
or removes a group member. The legacy provider's `asc_group_member` and
`asc_group_role` registrations therefore have no UAM equivalent and cannot be
reimplemented without new UAM API work.

## Read strategy forced by the contract

Because a missing role or group answers 502, the authoritative read is:

1. List (`GET .../roles` or `GET /v1/tenants/groups`). Absence from the list is
   the only reliable "gone" signal and is what removes Terraform state.
2. For an entry the list proves present, the single-item GET supplies what the
   list omits: `composites` for a role's actions, `members` for a group's
   membership. That second call is safe only because the list already proved
   the resource exists. A role's single-item GET is addressed by the tenant
   UUID, not the namespace; see "Tenant identifier".

A 502 from a single-item GET must never be treated as "not found", and a 404
must still be honoured if UAM ever starts returning one.

## Evidence

Read at ASC `72674111` ("Release a VM group's name when its deletion is
acknowledged") and the gitops checkout at the same date:
`services/uam-service/adapters/http/{handler,roles,group,errors,validation}.go`,
`services/uam-service/domain/{role,group}.go`,
`services/uam-service/domain/service/{role,group}.go`,
`services/uam-service/adapters/apiclients/authorization.go`,
`services/uam-service/adapters/postgres/role.go`,
`services/uam-service/adapters/http/middleware/security.go`,
`libs/httptransport/responses.go`, `libs/resources/urn.go`,
`applications/base/uam-service/{authorization-routes,securitypolicy}.yaml` and
`applications/overlays/dev/uam-service/httproute.yaml`.
