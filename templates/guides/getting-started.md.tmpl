---
page_title: "Getting started"
subcategory: ""
description: |-
  Configure the provider, pick a SKU, and create a VPC with a subnet and a virtual machine in it.
---

# Getting started

This guide takes an empty tenant to a running virtual machine: configure the provider, look up a
SKU, create a VPC with an inline subnet, and create a machine in it. For a complete end-to-end
configuration, see the README of the
[terraform-template-demo](https://github.com/NL-AMS-DSPC/terraform-template-demo) repository.

## Configure the provider and credentials

The provider authenticates with the OAuth client-credentials grant. `username` is the client ID and
`password` is the client secret. `org` is the realm the client belongs to, so the provider requests
its token from `<auth_url>/realms/<org>/protocol/openid-connect/token`. `namespace` is the tenant
to manage and is sent on every request as the `X-Tenant` header.

```terraform
terraform {
  required_providers {
    asc = {
      source = "NL-AMS-DSPC/asc"
    }
  }
}

provider "asc" {
  endpoint  = "https://api.example.com"
  auth_url  = "https://auth.example.com"
  org       = "example-org"
  username  = "terraform-client"
  namespace = "example-tenant"
}
```

Keep the secret out of the configuration and export it instead. Every argument has an environment
variable: `ASC_ENDPOINT`, `ASC_AUTH_URL`, `ASC_ORG`, `ASC_USERNAME`, `ASC_PASSWORD`,
`ASC_NAMESPACE` and `ASC_TIMEOUT` (HTTP timeout in seconds, default 30).

```shell
export ASC_PASSWORD='...'
terraform init
```

## Pick a SKU

The `asc_skus` data source lists the instance sizes the tenant can use. A machine takes the SKU's
`id` as `sku_id`.

```terraform
data "asc_skus" "available" {}

output "sku_ids" {
  value = [for sku in data.asc_skus.available.skus : sku.id]
}
```

## Create a VPC and a virtual machine

A VPC is created together with at least one inline subnet. The machine refers to the VPC by `id`,
which makes Terraform create the VPC first.

```terraform
resource "asc_vpc" "main" {
  name = "main"
  cidr = "10.0.0.0/16"

  subnets = [{
    name = "private"
    cidr = "10.0.0.0/24"
  }]
}

resource "asc_virtual_machine" "web" {
  name   = "web"
  sku_id = "gp-2"
  vpc_id = asc_vpc.main.id
  image  = "vm-images/ubuntu:24.04"
}

output "vm_status" {
  value = asc_virtual_machine.web.status
}

output "vm_urn" {
  value = asc_virtual_machine.web.urn
}
```

Machine and VPC names are 1 to 50 characters: a lowercase letter first, then lowercase letters,
digits or hyphens, ending with a letter or digit. Omit `image` to get `vm-images/ubuntu:24.04`, and set `boot_disk_size_gb` (default 32) to size the boot disk. Changing either replaces the machine.

## What apply and destroy do

ASC provisions asynchronously: a create call is only an acknowledgement. After each create the
provider polls the resource until it can be read and is not a leftover of an earlier resource with
the same name still being deleted. It fails immediately if the resource reports the `error` status,
and includes the reported `last_error` in the message. The whole wait is bounded at 10 minutes.
`status` is whatever ASC reports at that moment, so it can still be `pending` right after apply;
run `terraform apply -refresh-only` later to see the settled value.

On destroy the provider deletes the machine and polls it until ASC answers 404, and only then
deletes the VPC, so the VPC is never removed while a machine is still using it. A resource that is
already gone counts as deleted.

## Caveats

- Most arguments cannot be changed on a live resource. Changing a VPC name, `cidr` or inline subnet, or a
  machine name, `sku_id`, `vpc_id`, `image`, `boot_disk_size_gb` or `customization`, plans a
  replacement. `sku_id` replaces because ASC accepts only one resize per machine lifetime. Only
  `tags` update in place on VPCs, subnets and machines.
- A same-name replacement waits for the old resource to disappear before creating the new one.
- `customization` is sensitive and ASC never returns it, so the configured value is kept in state
  and cannot be recovered by import.
- Read the plan before applying: the full list of which attribute updates in place and which
  replaces is in
  [supported fields](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/supported-fields.md).
