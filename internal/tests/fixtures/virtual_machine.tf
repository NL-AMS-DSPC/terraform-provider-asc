resource "asc_virtual_machine" "test" {
  name           = "test-vm"
  sku_id         = "gp-2"
  vpc_id         = "00000000-0000-4000-8000-000000000001"
  image          = "ubuntu-24.04"
  enable_logging = true
  tags           = { env = "test" }
}
