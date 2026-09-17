resource "asc_volume_attachment" "example" {
  volume_name = asc_volume.example.name
  vm_name     = asc_virtual_machine.example.name
}
