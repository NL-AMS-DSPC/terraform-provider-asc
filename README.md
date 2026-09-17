# ASC Terraform Provider

This is the clean implementation of the ASC Terraform provider.

Its intentionally small boundary is:

- OAuth client-credentials authentication using the configured UAM/authentication endpoint.
- Infrastructure operations exclusively through `/api/infra-gateway`.
- The `asc_vpc`, `asc_subnet`, and `asc_virtual_machine` resources.
- The read-only `asc_skus` data source.

Legacy direct VM, network, storage, database, container, function, cluster,
security, load-balancer, role, and group service adapters are not carried into
this repository.

Wider resource parity, mutating lifecycle waiters beyond the core workflow, and
UAM identity and access resources remain deferred.

The provider retains the Terraform source address
`registry.terraform.io/nl-ams-dspc/asc` and the `asc_*` resource namespace.

## Development

```sh
make build
make test
```

`make test` enables Terraform acceptance tests. The focused tests used during
development are recorded in the Grid execution plan.
