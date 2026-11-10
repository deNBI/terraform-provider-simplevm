resource "simplevm_vm" "web" {
  name   = "web"
  flavor = "de.NBI default"
  image  = "Ubuntu-22.04"
}

# Snapshots cannot be deleted through the SimpleVM API: destroying this
# resource only removes it from state.
resource "simplevm_snapshot" "before_upgrade" {
  vm_id       = simplevm_vm.web.id
  name        = "web-before-upgrade"
  description = "State of the web VM before the upgrade"
}
