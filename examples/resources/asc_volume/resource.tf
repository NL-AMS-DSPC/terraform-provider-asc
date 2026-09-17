resource "asc_volume" "example" {
  name          = "example-data"
  size          = "10Gi"
  storage_class = "standard"
  access_mode   = "ReadWriteOnce"

  tags = {
    env = "production"
  }
}
