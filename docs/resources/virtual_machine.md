---
page_title: "asc_virtual_machine Resource"
---

# asc_virtual_machine

Creates a VM through infra-gateway. Names are 1-50 characters, start with a
lowercase letter, contain lowercase letters, digits, or hyphens, and end with a
letter or digit. All configurable fields require replacement.

`customization` is sensitive and is preserved from configuration during refresh
because infra-gateway does not return secret initialization content. Import
with the tenant-scoped VM name; imported VMs have no customization state.
