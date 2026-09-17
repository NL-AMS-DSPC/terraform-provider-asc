data "asc_vpc" "platform" {
  name = "platform"
}

output "platform_subnet_cidrs" {
  value = [for subnet in data.asc_vpc.platform.subnets : subnet.cidr]
}
