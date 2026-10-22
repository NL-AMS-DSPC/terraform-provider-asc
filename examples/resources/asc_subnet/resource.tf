# An additional subnet in an existing VPC. The CIDR must lie within the VPC's
# CIDR (10.0.0.0/24 in the asc_vpc example) and must not overlap its other
# subnets (10.0.0.0/25 there).
resource "asc_subnet" "example" {
  name     = "second"
  vpc_name = asc_vpc.example.name
  vpc_id   = asc_vpc.example.id
  cidr     = "10.0.0.128/25"
}
