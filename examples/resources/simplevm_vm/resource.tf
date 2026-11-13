# A VM from a base image.
resource "simplevm_vm" "base" {
  name   = "analysis"
  flavor = "de.NBI default"
  image  = "Ubuntu-22.04"
}

# Log in over SSH. SimpleVM copies the public key from your SimpleVM profile
# into the new VM, so set it there first; the private key stays with you.
variable "ssh_private_key_file" {
  type    = string
  default = "~/.ssh/id_ed25519"
}

output "ssh_command" {
  value = "ssh -i ${var.ssh_private_key_file} -p ${simplevm_vm.base.ssh_port} ${simplevm_vm.base.ssh_user}@${simplevm_vm.base.ssh_host}"
}

# Or run commands on the VM once it is created.
resource "terraform_data" "tools" {
  triggers_replace = [simplevm_vm.base.id]

  connection {
    type        = "ssh"
    host        = simplevm_vm.base.ssh_host
    port        = simplevm_vm.base.ssh_port
    user        = simplevm_vm.base.ssh_user
    private_key = file(pathexpand(var.ssh_private_key_file))
  }

  provisioner "remote-exec" {
    inline = ["sudo apt-get update", "sudo apt-get install -y htop"]
  }
}

# A VM with an RStudio research environment, created stopped.
data "simplevm_image" "rstudio" {
  name = "RStudio-Ubuntu-22.04"
}

resource "simplevm_vm" "rstudio" {
  name   = "rstudio"
  flavor = "de.NBI default"
  image  = data.simplevm_image.rstudio.name

  research_environment = {
    template = data.simplevm_image.rstudio.resenv_template_name
  }

  power_state = "stopped"

  timeouts = {
    create = "45m"
  }
}
