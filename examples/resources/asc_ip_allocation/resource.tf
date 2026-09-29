resource "asc_ip" "example" {}

resource "asc_ip_allocation" "example" {
  vm_name = asc_virtual_machine.example.name
  ip      = asc_ip.example.ip
}
