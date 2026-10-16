# Look up an existing subnet by its VPC name and subnet name, then place a
# virtual machine in the VPC that owns it.
data "asc_subnet" "public" {
  vpc_name = "platform"
  name     = "public"
}

resource "asc_virtual_machine" "edge" {
  name   = "edge"
  sku_id = "gp-2"
  vpc_id = data.asc_subnet.public.vpc_id
}
