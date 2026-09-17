resource "asc_virtual_machine" "example" {
  name     = "example-vm"
  sku_id   = "gp-2"
  vpc_id   = asc_vpc.example.id
  image    = "ubuntu-24.04"

  enable_logging = true
}
