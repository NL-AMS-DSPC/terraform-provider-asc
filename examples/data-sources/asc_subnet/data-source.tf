# Look up an existing subnet by its VPC name and subnet name.
data "asc_subnet" "public" {
  vpc_name = "platform"
  name     = "public"
}

output "public_subnet_cidr" {
  value = data.asc_subnet.public.cidr
}
