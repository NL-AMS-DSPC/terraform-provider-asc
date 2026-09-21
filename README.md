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

## Upgrading

Two builds can have written the state a user holds: the published
`NL-AMS-DSPC/dspc` v0.0.1 release, and the unreleased legacy build that already
answers to this provider's source address. They take different paths, and taking
the wrong one can cause unnecessary state removal or replacement plans.

- [docs/compatibility.md](docs/compatibility.md) is the matrix: addresses,
  protocols, schema versions, what changed shape, and what the first plan after
  the switch is allowed to do.
- [docs/guides/upgrade-runbook.md](docs/guides/upgrade-runbook.md) is the
  procedure: how to tell which origin you are on, how to back up and pin, what a
  safe dry run looks like, and the stop conditions that abort the upgrade.

The surface per version is in [CHANGELOG.md](CHANGELOG.md).

Nothing is published yet. No version has been tagged, `nl-ams-dspc/asc` is not on
the Terraform Registry, and publication -- creating the GitHub repository,
claiming the registry namespace, adding the signing key and pushing the first
tag -- is a separately approved step outside the work that produced this
repository. Until then the provider runs through `dev_overrides` or a filesystem
mirror; `make release-snapshot` and `make install-check` prove the packaging
locally with a throwaway key, without publishing.

## Development

```sh
make build
make test
```

`make test` enables Terraform acceptance tests against local mock services.

The build requires Go 1.27.1 or later and a Terraform CLI on `PATH`.
CI tests Terraform 1.5.7 and 1.16.3. `make test` uses that CLI for both
acceptance tests and migration fixtures; set `TF_ACC_TERRAFORM_PATH` to select
another binary. `make check` also checks formatting, module tidiness, workflow
syntax, generated documentation and known Go vulnerabilities. `make test-race`
runs the suite with the race detector.

Release checks require GnuPG, Python 3 and unzip. Their Go tools are pinned
in the Makefile, generation directive and release script; a first run needs
network access to download them. Provider installation uses a local mirror.

gRPC is pinned to the patched stable `v1.83.2`: the Go vulnerability database
still flags `v1.84.0` for [GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443).
Recheck that advisory before upgrading the pin.
