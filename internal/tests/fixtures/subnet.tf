resource "asc_subnet" "test" {
  name     = "test-subnet"
  vpc_name = "test-vpc"
  vpc_id   = "00000000-0000-4000-8000-000000000001"
  cidr     = "10.0.0.128/25"
  type     = "public"
}
