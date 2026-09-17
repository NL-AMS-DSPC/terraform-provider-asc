resource "asc_vpc" "example" {
  name = "example-vpc"

  subnets = [{
    name = "bootstrap"
    cidr = "10.0.0.0/25"
    type = "private"
  }]
}
