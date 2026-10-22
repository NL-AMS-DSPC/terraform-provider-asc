# A role granting two virtual machine actions. Actions form an unordered set.
resource "asc_role" "example" {
  name = "operator"

  actions = [
    "vm:create",
    "vm:restart",
  ]
}
