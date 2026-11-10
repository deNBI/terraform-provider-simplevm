resource "simplevm_vm" "this" {
  project_id  = var.project_id
  name        = var.name
  flavor      = var.flavor
  image       = var.image
  power_state = var.power_state

  research_environment = var.research_environment_template == null ? null : {
    template = var.research_environment_template
  }
}

resource "simplevm_volume" "data" {
  count = var.volume_size_gb == null ? 0 : 1

  project_id = var.project_id
  name       = "${var.name}-data"
  size       = var.volume_size_gb
}

resource "simplevm_volume_attachment" "data" {
  count = var.volume_size_gb == null ? 0 : 1

  volume_id = simplevm_volume.data[0].id
  vm_id     = simplevm_vm.this.id
}
