# A 10 GiB block volume. size takes a Kubernetes quantity and can only grow.
resource "asc_volume" "example" {
  name = "example-data"
  size = "10Gi"

  tags = {
    env = "production"
  }
}
