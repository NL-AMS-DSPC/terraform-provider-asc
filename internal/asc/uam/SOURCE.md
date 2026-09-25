# uam-service OpenAPI source

`openapi.json` is a verbatim copy of the Terraform-facing spec that ASC builds. Do not
edit it by hand; regenerate it in ASC and copy it here.

- ASC target: `//clients/uam-service:openapi3_terraform` (`bazel-bin/clients/uam-service/openapi3.terraform.json`)
- ASC revision: branch `grid/atos-mp6-generate-asc-go-clients-for-terraform`, commit `4f2818c2f34c22eb504d81c53c85472e7fdc63a7`
- sha256: `9f9e4ec6f381942d414cf31626a49b72db6be8174a87468d7e351e73ec4d8745`

The allowlist in ASC's `clients/uam-service/terraform-operations.json` decides which operations
the spec holds. After copying a new spec, update the revision and sha256 above and run
`make generate`; `make check-generated` fails when either drifts.
