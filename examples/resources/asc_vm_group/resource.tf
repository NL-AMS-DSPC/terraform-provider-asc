resource "asc_vm_group" "example" {
  name   = "example-group"
  sku_id = "gp-2"
  vpc_id = asc_vpc.example.id
  image  = "ubuntu-24.04"

  autoscaling = {
    min_replicas = 2
    max_replicas = 10

    cron = {
      timezone         = "Europe/Amsterdam"
      start            = "0 8 * * 1-5"
      end              = "0 18 * * 1-5"
      desired_replicas = 6
    }
  }

  tags = {
    env = "production"
  }
}
