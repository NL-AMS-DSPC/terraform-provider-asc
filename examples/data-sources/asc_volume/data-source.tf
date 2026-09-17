data "asc_volume" "example" {
  name = "example-data"
}

output "volume_size_bytes" {
  value = data.asc_volume.example.size_bytes
}
