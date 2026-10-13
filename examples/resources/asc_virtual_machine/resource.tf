# A virtual machine inside the VPC from the asc_vpc example. List valid
# sku_id values with the asc_skus data source.
resource "asc_virtual_machine" "example" {
  name   = "example-vm"
  sku_id = "gp-2"
  vpc_id = asc_vpc.example.id

  # Optional: ubuntu-24.04-lts when omitted.
  image = "ubuntu-24.04-lts"

  # Optional: size of the boot disk in GB, 32 when omitted.
  boot_disk_size_gb = 32

  enable_logging = true

  tags = {
    env = "production"
  }
}
