resource "asc_virtual_machine" "test" {
  name   = "test-vm"
  sku_id = "gp-2"
  vnics  = ["asc:atos:network:test-tenant:vnic:00000000-0000-4000-8000-000000000021"]
  image  = "ubuntu-24.04"
  tags   = { env = "test" }
}
