resource "asc_role" "example" {
  name = "operator"

  actions = [
    "vm:read",
    "vm:write",
  ]
}
