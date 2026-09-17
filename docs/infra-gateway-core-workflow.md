# Minimal infra-gateway Terraform contract

The fresh provider has one infrastructure boundary: `/api/infra-gateway`.
Every request obtains an OAuth client-credentials token, sends it as a bearer
token, and sends the configured namespace as `X-Tenant`. Successful JSON
responses are decoded from their `data` envelope.

| Workflow requirement | Provider contract | ASC status |
| --- | --- | --- |
| VPC create/read/delete | `POST /vpcs/`, `GET /vpcs/{name}`, `DELETE /vpcs/{name}` | Existing handler contract |
| Inline bootstrap subnet | VPC create requires at least one subnet and omits unresolved `vpcID` | Provider-only encoding rule |
| Standalone subnet create/read/delete | `POST /vpcs/{vpc}/subnets/`, list and exact-name match, `DELETE /vpcs/{vpc}/subnets/{name}` | Existing handler contract |
| VM create/read/delete | `POST /vms/`, `GET /vms/{name}`, `DELETE /vms/{name}` | Existing handler plus approved GET fields below |
| Authoritative VM VPC and logging state | GET returns `vpcID` and `enableLogging` | Approved ASC delta in `vms.go` and `vms_test.go` |
| VM customization refresh | Preserve sensitive configured value because GET deliberately does not return it | Provider-only state rule |
| Missing resources | Only HTTP 404 removes Terraform state or makes delete idempotent | Provider-only typed error rule |
| Unsupported updates | Every configurable VPC, subnet, and VM field requires replacement | Provider schema rule |
| Canonical VM names | 1-50 chars, lowercase letter first, then lowercase letters/digits/hyphens, alphanumeric last | Provider validation matching ASC |
| Same-name VPC/VM replacement | Delete converges; typed 409 plus authoritative GET distinguishes stale reservations from real conflicts | Approved ASC error-mapping delta plus provider lifecycle rule |

The approved ASC change maps only existing `ErrVPCNameNotUnique` and
`ErrVMNameNotUnique` service errors to HTTP 409 in `respondWithServiceError`,
with focused assertions. It changes no endpoint, payload, event, repository,
projector, GitOps, or generated contract. The provider retries a conflict only
when authoritative GET state proves the old resource is deleting or absent.

The corresponding provider algorithm is deliberately narrow:

1. After VPC or VM DELETE succeeds, poll its authoritative GET until 404.
2. Continue while status is `pending`, `active`, or `deleting`; fail immediately
   on status `error` and include `lastError` in the diagnostic.
3. Propagate authentication, validation, transport, and non-404 server errors;
   obey Terraform cancellation and a bounded deletion deadline.
4. If the following same-name POST returns typed 409, GET the name. An existing
   non-deleting resource is a real conflict. A deleting resource returns to the
   deletion wait. A 404 is a stale uniqueness reservation, so retry only that
   POST until success or the bounded deadline.

Focused unit coverage must prove cancellation, deadline, terminal-error and
non-404 behavior. Stateful acceptance coverage must simulate deleting reads,
GET 404, one stale 409, and successful same-name recreation for both VPC and VM.

The fresh repository contains no direct VM, network, block/file/object storage,
database, function, container, cluster, security, load-balancer, role, or group
client. Authentication token acquisition remains; the old `/api/authorization`
client is not treated as UAM without a separate authoritative contract.
