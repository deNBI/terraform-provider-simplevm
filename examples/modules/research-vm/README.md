# research-vm example module

Creates a SimpleVM virtual machine, optionally with a research environment and
an attached data volume. It is an example of composing the provider's
resources, not a published module: copy it into your project.

```hcl
module "rstudio" {
  source = "./modules/research-vm"

  name                          = "rstudio"
  flavor                        = "de.NBI default"
  image                         = "RStudio-Ubuntu-22.04"
  research_environment_template = "rstudio"
  volume_size_gb                = 100
}

output "url" {
  value = module.rstudio.research_environment_url
}
```

## Inputs

| Name | Type | Default | Description |
|------|------|---------|-------------|
| `name` | string | required | Name of the VM, 1 to 27 characters; the volume is named `<name>-data`, and SimpleVM allows at most 32 characters for volume names. |
| `project_id` | number | `null` | SimpleVM project ID; defaults to the provider's. |
| `flavor` | string | required | Flavor name. |
| `image` | string | required | Base image or snapshot image name. |
| `research_environment_template` | string | `null` | Research environment template; `null` for a plain VM. |
| `volume_size_gb` | number | `null` | Size of a data volume in GiB; `null` for no volume. |
| `power_state` | string | `"active"` | `active` or `stopped`. |

## Outputs

| Name | Description |
|------|-------------|
| `vm_id` | UUID of the VM. |
| `fixed_ip` | Fixed (internal) IP address. |
| `research_environment_url` | URL of the research environment (empty without one). |
| `volume_id` | UUID of the data volume, or `null`. |

Attaching a volume does not format or mount it. Do that once inside the VM as
described in the [SimpleVM volumes documentation](https://simplevm.denbi.de/wiki/simple_vm/volumes/).
