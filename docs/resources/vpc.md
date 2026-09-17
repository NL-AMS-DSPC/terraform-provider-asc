---
page_title: "asc_vpc Resource"
---

# asc_vpc

Creates a VPC through infra-gateway. At least one inline bootstrap subnet is
required. Inline subnet ownership stays with this resource; standalone subnets
must use `asc_subnet` and must not duplicate an inline subnet name.

All configurable fields require replacement. Import with the tenant-scoped VPC
name.
