resource "asc_ip" "example" {}

output "example_reserved_ip" {
  value = asc_ip.example.ip
}
