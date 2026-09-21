resource "asc_vpc" "test" {
  name    = "test-vpc"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
  tags    = { env = "test" }
}
