# Attach the volume from the asc_volume example to the machine from the
# asc_virtual_machine example. Moving it to another machine replaces it.
resource "asc_volume_attachment" "example" {
  volume_name = asc_volume.example.name
  vm_name     = asc_virtual_machine.example.name
}
