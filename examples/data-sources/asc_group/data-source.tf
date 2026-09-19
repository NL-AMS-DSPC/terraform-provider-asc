data "asc_group" "example" {
  name = "platform"
}

output "example_group_member_names" {
  value = [for member in data.asc_group.example.members : member.name]
}
