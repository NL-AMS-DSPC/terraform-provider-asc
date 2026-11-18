# A virtual machine attached through the VNIC from the asc_vnic example. List
# valid sku_id values with the asc_skus data source.
resource "asc_virtual_machine" "example" {
  name   = "example-vm"
  sku_id = "gp-2"
  vnics  = [asc_vnic.example.urn]

  # Optional: ubuntu-24.04-lts when omitted.
  image = "ubuntu-24.04-lts"

  tags = {
    env = "production"
  }
}
