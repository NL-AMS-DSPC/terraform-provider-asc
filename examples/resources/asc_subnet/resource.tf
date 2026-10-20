resource "asc_subnet" "example" {
  name     = "public"
  vpc_name = asc_vpc.example.name
  vpc_id   = asc_vpc.example.id
  cidr     = "10.0.0.128/25"
}
