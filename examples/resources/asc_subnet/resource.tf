# An additional subnet in an existing VPC. The CIDR must not overlap the
# subnets the VPC was created with (10.0.0.0/25 in the asc_vpc example).
resource "asc_subnet" "example" {
  name     = "public"
  vpc_name = asc_vpc.example.name
  vpc_id   = asc_vpc.example.id
  cidr     = "10.0.0.128/25"
  type     = "public" # "public" or "private"
}
