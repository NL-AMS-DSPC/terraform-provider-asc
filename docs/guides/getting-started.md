---
page_title: "Getting started"
subcategory: ""
description: |-
  Configure the provider, pick a SKU, and create a VPC with a subnet and a virtual machine attached to it.
---

# Getting started

This guide takes an empty tenant to a running virtual machine: configure the provider, look up a
SKU, create a VPC with an inline subnet, and create a machine attached to it. For a complete end-to-end
configuration, see the README of the
[terraform-template-demo](https://github.com/NL-AMS-DSPC/terraform-template-demo) repository.

## Configure the provider and credentials

The provider authenticates with the OAuth client-credentials grant. `username` is the client ID and
`password` is the client secret. `org` is the realm the client belongs to, and `namespace` is the
tenant to manage.

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

A VPC has a CIDR and is created together with at least one inline subnet inside it. A machine
connects to a subnet through virtual network interfaces (VNICs): each `asc_vnic` takes a private
IP from the subnet it names, and the machine lists the VNICs to attach by `urn`. The references
make Terraform create the VPC, then the VNIC, then the machine.

```terraform
resource "asc_vpc" "main" {
  name = "main"
  cidr = "10.0.0.0/16"

  subnets = [{
    name = "private"
    cidr = "10.0.0.0/24"
  }]
}

resource "asc_vnic" "web" {
  subnet_urn = asc_vpc.main.subnets[0].urn
}

resource "asc_virtual_machine" "web" {
  name   = "web"
  sku_id = "gp-2"
  vnics  = [asc_vnic.web.urn]
  image  = "ubuntu-24.04-lts"
}

output "vm_status" {
  value = asc_virtual_machine.web.status
}

output "vm_urn" {
  value = asc_virtual_machine.web.urn
}

output "vm_private_ip" {
  value = asc_vnic.web.private_ip
}
```

A machine can have several VNICs, in different subnets; list them in the order the machine should
see them.

Machine and VPC names are 1 to 50 characters: a lowercase letter first, then lowercase letters,
digits or hyphens, ending with a letter or digit. Omit `image` to get `ubuntu-24.04-lts`.

## What apply and destroy do

Apply waits until each new resource exists, until a new VPC is active and until a new VNIC has its
private IP, for up to 10 minutes. A
resource that reports the `error` status fails the apply with its `last_error`. A machine's
`status` can still be `pending` right after apply; run `terraform apply -refresh-only` later to see
the settled value.

Destroy removes the machine before its VNICs, and the VNICs before the VPC. A VNIC still attached
to a machine cannot be deleted, so the provider retries its deletion until the machine has released
it.

## Caveats

- Most arguments cannot be changed on a live resource. Changing a VPC name, CIDR or inline subnet, or a
  machine name, `sku_id`, `vnics`, `image` or `customization`, or a VNIC's `subnet_urn`, plans a
  replacement. Only `tags` update in place on VPCs, subnets and machines; a VNIC can also be
  renamed in place.
- `customization` is sensitive and cannot be recovered by import.
- Read the plan before applying: the full list of which attribute updates in place and which
  replaces is in
  [supported fields](https://github.com/NL-AMS-DSPC/terraform-provider-asc/blob/main/docs/supported-fields.md).
