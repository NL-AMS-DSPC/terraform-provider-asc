# A virtual machine attached to the VPC from the asc_vpc example. List valid
# sku_id values with the asc_skus data source.
resource "asc_virtual_machine" "example" {
  name   = "example-vm"
  sku_id = "gp-2"
  vpc_id = asc_vpc.example.id

  # Optional: vm-images/ubuntu:24.04 when omitted.
  image = "vm-images/ubuntu:24.04"

  tags = {
    env = "production"
  }
}
