# infra-gateway OpenAPI source

`openapi.json` is a verbatim copy of the Terraform-facing spec that ASC builds. Do not
edit it by hand; regenerate it in ASC and copy it here.

- ASC target: `//clients/infra-gateway:openapi3_terraform` (`bazel-bin/clients/infra-gateway/openapi3.terraform.json`)
- ASC revision: branch `grid/atos-mp6-generate-asc-go-clients-for-terraform`, commit `4f2818c2f34c22eb504d81c53c85472e7fdc63a7`
- sha256: `e1abced89b42ec7c7da86277eaf50ad2bf9a6c8782fa3fc3efe3529149b259e3`

The allowlist in ASC's `clients/infra-gateway/terraform-operations.json` decides which operations
the spec holds. After copying a new spec, update the revision and sha256 above and run
`make generate`; `make check-generated` fails when either drifts.
