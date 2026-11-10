resource "simplevm_volume_attachment" "data" {
  volume_id = simplevm_volume.data.id
  vm_id     = simplevm_vm.web.id
}
