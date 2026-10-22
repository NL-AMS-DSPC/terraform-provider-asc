# Look up an existing subnet by its VPC name and subnet name, then attach
# a virtual machine's network interface to that subnet.
data "asc_subnet" "public" {
  vpc_name = "platform"
  name     = "public"
}

resource "asc_virtual_machine" "edge" {
  name   = "edge"
  sku_id = "gp-2"
  vpc_id = data.asc_subnet.public.vpc_id
}
