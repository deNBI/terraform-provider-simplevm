# A VM is imported by its SimpleVM UUID. research_environment is not restored
# on import; add `lifecycle { ignore_changes = [research_environment] }` to the
# resource if the VM has one.
terraform import simplevm_vm.base 3f2a9c4e-1b7d-4e8a-9c21-5d6f7a8b9c0d
