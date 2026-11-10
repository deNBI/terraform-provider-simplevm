# A VM from a base image.
resource "simplevm_vm" "base" {
  name   = "analysis"
  flavor = "de.NBI default"
  image  = "Ubuntu-22.04"
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
