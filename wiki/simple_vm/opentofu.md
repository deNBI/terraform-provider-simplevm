# OpenTofu and Terraform

This page explains how to manage SimpleVM resources as code with OpenTofu or Terraform.

Instead of clicking through the portal, you describe your virtual machines, volumes and snapshots in a short text file. One command creates everything, and another one removes it again. That makes your setup repeatable: you can recreate an analysis environment, prepare machines for a workshop or keep your project's setup under version control.

The `deNBI/simplevm` provider works with both [OpenTofu](https://opentofu.org/) and [Terraform](https://developer.hashicorp.com/terraform). The examples below use the `tofu` command. If you use Terraform, replace `tofu` with `terraform`.

!!! info "Beta"
    The provider is new and relies on the SimpleVM API keys, which are a beta feature. Please report problems on [GitHub](https://github.com/deNBI/terraform-provider-simplevm/issues).

## Prerequisites

You need a SimpleVM project and an API key for it.

!!! info "API keys are a beta feature"
    API keys are available on request. See [User API keys](user_api_keys.md) for how to ask for access and how to create a key.

When you create the key in the **API Key** tab of your project:

1. Select the scopes the provider needs. Without them, requests fail with a "forbidden" error:
    - `project:read` to look up flavors, images and snapshots,
    - `vm:read` and the scopes for creating, stopping, resuming and deleting VMs,
    - the scopes for creating and deleting volumes,
    - `volume:change_state` to attach, detach and extend volumes.
2. Choose an expiry date. When the key expires, the provider can no longer manage your resources, so create a new key and export it.
3. Keep "Own Objects Only" switched on if you only want to manage objects created with this key (see the FAQ below).
4. Copy the key right away. SimpleVM shows it only once.

!!! warning "Keep your key secret"
    Anyone with the key can act in your project within its scopes. Do not write it into files that you share or commit to git. Use an environment variable as shown below.

The owner of the API key becomes the owner of the VMs created with it.

## Installing OpenTofu or Terraform

Install one of them by following the official instructions:

- OpenTofu: [opentofu.org/docs/intro/install](https://opentofu.org/docs/intro/install/)
- Terraform: [developer.hashicorp.com/terraform/install](https://developer.hashicorp.com/terraform/install)

You need OpenTofu 1.6 or newer, or Terraform 1.5 or newer. Check with:

```bash
tofu version
```

The provider itself is downloaded automatically by `tofu init`. For OpenTofu and Terraform the declaration is the same:

```hcl
terraform {
  required_providers {
    simplevm = {
      source = "deNBI/simplevm"
    }
  }
}
```

!!! info "Not yet released"
    The provider is not published to the OpenTofu and Terraform registries yet. Once it is released, `tofu init` installs it from there. Until then you can build it yourself; see the [developer documentation](https://github.com/deNBI/terraform-provider-simplevm/blob/main/development/testing.md) on `dev_overrides`.

## Quickstart

1. Create an empty directory and put the following into a file named `main.tf`. Replace the flavor and image names with ones from your project (see the portal's VM creation dialog):

    ```hcl
    terraform {
      required_providers {
        simplevm = {
          source = "deNBI/simplevm"
        }
      }
    }

    provider "simplevm" {}

    resource "simplevm_vm" "hello" {
      name   = "hello-opentofu"
      flavor = "de.NBI default"
      image  = "Ubuntu 22.04"
    }

    output "ip" {
      value = simplevm_vm.hello.fixed_ip
    }
    ```

2. Tell the provider your key and project. The project ID is the number shown in the portal for your project:

    ```bash
    export SIMPLEVM_API_KEY="paste-your-key-here"
    export SIMPLEVM_PROJECT_ID="1234"
    ```

3. Download the provider:

    ```bash
    tofu init
    ```

4. Look at what would happen. Nothing is created yet:

    ```bash
    tofu plan
    ```

5. Create the VM. Type `yes` when asked. The command returns when the VM is running, which can take several minutes:

    ```bash
    tofu apply
    ```

6. When you do not need the VM any more, remove everything defined in the file:

    ```bash
    tofu destroy
    ```

!!! warning "Destroy deletes"
    `tofu destroy` deletes the VMs and volumes in your configuration, including the data on them. Snapshots are the exception, see below.

OpenTofu remembers what it created in a file named `terraform.tfstate` next to your configuration. Keep that file; without it OpenTofu does not know about the resources and would create new ones.

## Recipes

The recipes build on each other. Each snippet goes into `main.tf` in addition to the provider block shown above.

### A VM from a base image

```hcl
resource "simplevm_vm" "base" {
  name   = "analysis"
  flavor = "de.NBI default"
  image  = "Ubuntu 22.04"
}
```

Changing `name`, `flavor`, `image` or the research environment of an existing VM **replaces** it: the old VM is deleted and a new one is created. Always read the plan before you apply.

### A VM with a research environment

Images that support a research environment have a template name. Look it up with the `simplevm_image` data source and pass it on:

```hcl
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

  timeouts = {
    create = "45m"
  }
}

output "rstudio_url" {
  value = simplevm_vm.rstudio.research_environment_url
}
```

To see which images and templates exist, list them:

```hcl
data "simplevm_images" "all" {}

output "images" {
  value = data.simplevm_images.all.images
}
```

`tofu apply` waits until the research environment is installed, not only until the VM runs. That takes longer, so give it a generous `create` timeout as above. If the installation fails, the VM is marked as tainted: the next `tofu apply` deletes it and starts over.

!!! info "Timeouts"
    Timeouts are set with `timeouts = { create = "45m" }`. Defaults are 30 minutes for creating a VM, 15 minutes for changing or deleting it and 10 minutes for volume and snapshot operations.

### Add a volume

A volume is created separately and then attached to a VM:

```hcl
resource "simplevm_volume" "data" {
  name = "analysis-data"
  size = 100 # GiB
}

resource "simplevm_volume_attachment" "data" {
  volume_id = simplevm_volume.data.id
  vm_id     = simplevm_vm.base.id
}
```

Attaching only connects the volume to the VM. The first time, you still have to format and mount it inside the VM as described in [Volumes](volumes.md).

You can **grow** a volume by increasing `size`; this happens in place and keeps your data. Making `size` smaller is not possible in place: the volume is replaced by a new, empty one.

!!! warning "Shrinking a volume destroys its data"
    If you reduce `size`, `tofu plan` shows the volume as "must be replaced". Applying that deletes the volume and all data on it.

Deleting a VM detaches its volumes and keeps them.

### Stop and start a VM

Set `power_state` to `stopped` or `active`:

```hcl
resource "simplevm_vm" "base" {
  name        = "analysis"
  flavor      = "de.NBI default"
  image       = "Ubuntu 22.04"
  power_state = "stopped"
}
```

Run `tofu apply`; the VM is stopped in place. Change the value back to `active` to resume it. After a restart you may need to mount volumes again, see [Volumes](volumes.md).

### Take a snapshot

```hcl
resource "simplevm_snapshot" "before_upgrade" {
  vm_id       = simplevm_vm.base.id
  name        = "analysis-before-upgrade"
  description = "State before the upgrade"
}
```

`tofu apply` returns when the snapshot is available as an image. You can then create new VMs from it with `image = "analysis-before-upgrade"`.

!!! warning "Destroying a snapshot resource does not delete the snapshot"
    SimpleVM offers no way to delete snapshots through the API. `tofu destroy` only forgets the snapshot and prints a warning. Delete the snapshot yourself in the portal under Images/Snapshots.

!!! warning "If creating a snapshot times out"
    If `tofu apply` reports a timeout while creating a snapshot, the snapshot may still be created in the background. Check the portal under Images/Snapshots **before** you apply again. A second apply would create another snapshot that you cannot remove through OpenTofu.

Snapshots cannot be imported.

### Use the example module

The repository contains a small module that combines a VM, an optional research environment and an optional volume: [`examples/modules/research-vm`](https://github.com/deNBI/terraform-provider-simplevm/tree/main/examples/modules/research-vm). Copy it into your project and use it like this:

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

## Importing existing resources

If you already created a VM or a volume in the portal, you can bring it under OpenTofu's control. Write the matching resource block first, then import by the UUID shown in the portal:

```bash
tofu import simplevm_vm.base 3f2a9c4e-1b7d-4e8a-9c21-5d6f7a8b9c0d
tofu import simplevm_volume.data 3f2a9c4e-1b7d-4e8a-9c21-5d6f7a8b9c0d
tofu import simplevm_volume_attachment.data 3f2a9c4e-1b7d-4e8a-9c21-5d6f7a8b9c0d/7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d
```

The ID of an attachment is `<volume_id>/<vm_id>`.

Run `tofu plan` afterwards and check that it shows no changes.

!!! warning "VMs with a research environment"
    An import cannot recover the research environment of a VM. If your VM has one, leave `research_environment` out of the resource block and add the following; otherwise OpenTofu would want to replace the VM:

    ```hcl
    lifecycle {
      ignore_changes = [research_environment]
    }
    ```

## Limitations and FAQ

**Can I create clusters (BiBiGrid)?** No. Create and manage clusters in the SimpleVM portal.

**Can I add members to a VM or manage access?** No. The owner of the API key owns the VMs it creates. Share access in the portal.

**Which things are out of scope?** Workshops, port and security group changes, rebooting and rescuing VMs.

**What does "own objects only" mean for my key?** With it switched on (the default), the key only reaches objects created with this key. VMs and volumes that others (or you, in the portal) created may not be visible to it, so importing them can fail. Switch it off only if you are sure you want the key to reach everything in the project.

**`tofu apply` stops with a timeout.** Creating machines can take longer than expected. Raise the limit, for example `timeouts = { create = "60m" }`. A VM or volume whose creation timed out is marked as tainted, so the next `tofu apply` deletes it and creates it again; check the plan. For snapshots, check the portal first (see above).

**I get "forbidden" or "unauthorized".** The key is wrong, expired, belongs to another project or lacks a scope. Create a new key with the scopes listed under Prerequisites.

**I deleted something in the portal.** The next `tofu plan` notices and proposes to create it again.

**Where is my key stored?** Only in your environment. Do not commit `terraform.tfstate` files to a public repository either: they can contain details of your machines.

## More information

- Reference documentation of all resources and data sources: [OpenTofu registry](https://search.opentofu.org/provider/deNBI/simplevm/latest) and [Terraform registry](https://registry.terraform.io/providers/deNBI/simplevm/latest/docs) (available once the provider is released)
- Source code, developer documentation and issues: [github.com/deNBI/terraform-provider-simplevm](https://github.com/deNBI/terraform-provider-simplevm)
- [User API keys](user_api_keys.md)
- [Volumes](volumes.md)
