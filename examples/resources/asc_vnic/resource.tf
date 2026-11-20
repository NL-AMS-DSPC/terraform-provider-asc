# A virtual network interface in the inline subnet of the asc_vpc example. It
# gets its private IP from that subnet; attach it by listing its urn in an
# asc_virtual_machine's vnics.
resource "asc_vnic" "example" {
  subnet_urn = asc_vpc.example.subnets[0].urn

  # Optional: ASC chooses a name when it is omitted.
  name = "example-eth0"

  tags = {
    env = "production"
  }
}
