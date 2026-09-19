data "asc_role" "example" {
  name = "operator"
}

output "example_role_actions" {
  value = data.asc_role.example.actions
}
