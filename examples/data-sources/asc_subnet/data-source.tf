data "asc_subnet" "public" {
  vpc_name = "platform"
  name     = "public"
}

resource "asc_virtual_machine" "edge" {
  name   = "edge"
  sku_id = "gp-small"
  vpc_id = data.asc_subnet.public.vpc_id
}
