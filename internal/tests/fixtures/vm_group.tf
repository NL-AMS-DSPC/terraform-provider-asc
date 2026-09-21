resource "asc_vm_group" "test" {
  name    = "test-group"
  sku_id  = "gp-2"
  vpc_id  = "00000000-0000-4000-8000-000000000001"
  image   = "ubuntu-24.04"
  tags    = { env = "test" }
  autoscaling = {
    min_replicas = 2
    max_replicas = 6
    cron         = { timezone = "Europe/Amsterdam", start = "0 8 * * 1-5", end = "0 18 * * 1-5", desired_replicas = 4 }
  }
}
