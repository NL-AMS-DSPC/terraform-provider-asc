# A group of identical virtual machines that scales between 2 and 10
# replicas, and is held at 6 replicas on weekdays from 08:00 to 18:00.
# Changing any argument of a group replaces it.
resource "asc_vm_group" "example" {
  name   = "example-group"
  sku_id = "gp-2"
  vpc_id = asc_vpc.example.id
  image  = "vm-images/ubuntu:24.04"

  boot_disk_size_gb = 32

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
