data "asc_vm_group" "example" {
  name = "example-group"
}

output "example_group_replicas" {
  value = data.asc_vm_group.example.autoscaling.max_replicas
}
