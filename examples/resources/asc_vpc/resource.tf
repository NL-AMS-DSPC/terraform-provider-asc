resource "asc_vpc" "example" {
  name = "example-vpc"
  cidr = "10.0.0.0/16"

  subnets = [{
    name = "bootstrap"
    cidr = "10.0.0.0/25"
  }]
}
