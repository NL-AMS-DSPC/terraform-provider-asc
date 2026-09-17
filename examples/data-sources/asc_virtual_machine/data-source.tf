data "asc_virtual_machine" "bastion" {
  name = "bastion"
}

output "bastion_cores" {
  value = data.asc_virtual_machine.bastion.sku.cores
}
