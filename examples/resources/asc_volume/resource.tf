# A 10 GiB block volume. size takes a Kubernetes quantity and can only grow.
resource "asc_volume" "example" {
  name          = "example-data"
  size          = "10Gi"
  storage_class = "standard"
  access_mode   = "ReadWriteOnce" # or ReadWriteMany, ReadOnlyMany

  tags = {
    env = "production"
  }
}
