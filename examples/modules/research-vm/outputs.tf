output "vm_id" {
  description = "UUID of the VM."
  value       = simplevm_vm.this.id
}

output "fixed_ip" {
  description = "Fixed (internal) IP address of the VM."
  value       = simplevm_vm.this.fixed_ip
}

output "research_environment_url" {
  description = "URL of the research environment; empty for VMs without one."
  value       = simplevm_vm.this.research_environment_url
}

output "volume_id" {
  description = "UUID of the data volume, or null if none was requested."
  value       = one(simplevm_volume.data[*].id)
}
