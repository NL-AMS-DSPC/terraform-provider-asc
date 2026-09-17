data "asc_skus" "available" {}

output "sku_names" {
  value = [for sku in data.asc_skus.available.skus : sku.name]
}
