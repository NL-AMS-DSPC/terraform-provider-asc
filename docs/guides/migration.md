---
page_title: "Migrating from terraform-provider-dspc"
subcategory: ""
description: |-
  Why terraform-provider-asc is a fresh repository and what was intentionally left behind.
---

# Migrating from terraform-provider-dspc

`terraform-provider-asc` is a fresh repository, not a rename or a cleanup of the older
`terraform-provider-dspc`. The old repository remains read-only legacy and migration evidence.

## Why a fresh repository

Most of the legacy provider was no longer needed. Starting clean is the shortest way to enforce the
two-service architecture: Terraform connects only to infra-gateway and UAM. The legacy provider
hand-coded clients for `/api/vm`, `/api/network`, `/api/mdb`, `/api/authorization`, `/api/cluster`,
`/api/file`, `/api/object-storage`, `/api/containers` and `/api/serverless-container`. None of those
transports were copied here, and the old `/api/authorization` client is not proven UAM, so role and
group resources are not ported without a separate authoritative UAM contract.

## What is preserved

Provider type and resource names keep the `asc` / `asc_*` spelling, and the intended registry source
stays `NL-AMS-DSPC/asc`, so the Terraform source address does not change and no `replace-provider`
state surgery is required.

## Supported surface

The provider talks to infra-gateway under `/api/infra-gateway` with a bearer token from UAM and an
`X-Tenant` header, and decodes the `{"data": ...}` response envelope. HTTP 404 and 409 are mapped to
typed errors. Wider resource parity, load balancers and UAM identity/access resources remain
deferred; the supported surface is documented per release.
