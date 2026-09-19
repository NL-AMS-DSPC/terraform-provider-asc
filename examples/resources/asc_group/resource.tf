resource "asc_group" "example" {
  name = "platform"
}

output "example_group_members" {
  value = asc_group.example.members
}
