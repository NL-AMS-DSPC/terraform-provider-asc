# A VPC is created together with at least one inline subnet, which must lie
# within the VPC's CIDR.
resource "asc_vpc" "example" {
  name = "example-vpc"
  cidr = "10.0.0.0/24"

  subnets = [{
    name = "bootstrap"
    cidr = "10.0.0.0/25"
  }]
}
