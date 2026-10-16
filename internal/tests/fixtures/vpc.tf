resource "asc_vpc" "test" {
  name    = "test-vpc"
  cidr    = "10.0.0.0/24"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
  tags    = { env = "test" }
}
