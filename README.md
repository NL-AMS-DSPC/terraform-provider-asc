# ASC Terraform Provider

This is the clean implementation of the ASC Terraform provider.

Its intentionally small boundary is:

- OAuth client-credentials authentication against the configured authentication endpoint.
- Infrastructure operations exclusively through `/api/infra-gateway`.
- Roles and groups exclusively through `/api/uam`.

Those two API prefixes, plus the realm token path used to obtain a bearer token,
are the whole of the provider's egress. The acceptance suite records every route
the provider sends and fails on anything outside that allowlist, so a legacy
service prefix cannot be reintroduced by accident.

## Surface

| Resources | Data sources |
| --- | --- |
| `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_volume_attachment`, `asc_role`, `asc_group` | `asc_skus`, `asc_vpc`, `asc_subnet`, `asc_virtual_machine`, `asc_vm_group`, `asc_volume`, `asc_role`, `asc_group` |

Per-attribute change, import and read behaviour is in
[docs/supported-fields.md](docs/supported-fields.md). The UAM route contract and
the constraints it forces are in [docs/uam-contract.md](docs/uam-contract.md).

Legacy direct VM, network, storage, database, container, function, cluster,
security, load-balancer and authorization service adapters are not carried into
this repository. There is no `asc_load_balancer`, because infra-gateway
registers no read route for one, and no `asc_group_member` or `asc_group_role`,
because UAM exposes no membership or role-assignment route. The disposition of
all 41 legacy registrations is recorded in
[docs/legacy-registrations.md](docs/legacy-registrations.md), and what to do per
capability is in [docs/guides/migration.md](docs/guides/migration.md).

The provider retains the Terraform source address
`registry.terraform.io/nl-ams-dspc/asc` and the `asc_*` resource namespace.

## Development

```sh
make build
make test
```

`make test` enables Terraform acceptance tests. The focused tests used during
development are recorded in the Grid execution plan.
