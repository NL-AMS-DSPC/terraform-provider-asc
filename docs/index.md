---
page_title: "ASC Provider"
description: |-
  Configure the ASC provider for UAM authentication and infra-gateway resources.
---

# ASC Provider

The provider authenticates with OAuth client credentials and sends all
infrastructure operations to `/api/infra-gateway`. It does not connect directly
to VM, network, storage, or other infrastructure services.

```terraform
provider "asc" {
  endpoint  = "https://api.example.com"
  namespace = "tenant-name"
  auth_url  = "https://auth.example.com"
  org       = "realm-name"
  username  = "client-id"
  password  = var.asc_client_secret
}
```

Each setting can instead be supplied through its corresponding `ASC_*`
environment variable. `password` is sensitive in Terraform diagnostics and
should normally come from `ASC_PASSWORD`; it is never written to resource state.
