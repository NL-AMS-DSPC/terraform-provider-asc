data "asc_ips" "reserved" {}

output "reserved_ips" {
  value = [for ip in data.asc_ips.reserved.ips : ip.ip]
}
