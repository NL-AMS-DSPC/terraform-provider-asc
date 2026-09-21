# The released provider's configuration, unchanged. Nothing in it names
# nl-ams-dspc/asc, which is the point: this is what a v0.0.1 user actually has.
terraform {
  required_providers {
    dspc = {
      source = "NL-AMS-DSPC/dspc"
    }
  }
}

resource "dspc_virtual_machine" "test" {
  name = "test-vm"
}
