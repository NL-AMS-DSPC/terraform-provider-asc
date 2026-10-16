# Reserves a random IP address; it takes no arguments.
resource "asc_ip" "example" {}

output "example_reserved_ip" {
  value = asc_ip.example.ip
}
