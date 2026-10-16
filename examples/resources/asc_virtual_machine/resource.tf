resource "asc_virtual_machine" "example" {
  name     = "example-vm"
  sku_id   = "gp-2"
  vpc_id   = asc_vpc.example.id
  image    = "vm-images/ubuntu:24.04"
}
