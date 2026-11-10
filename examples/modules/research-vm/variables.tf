variable "name" {
  type        = string
  description = "Name of the VM, 1 to 27 characters. The volume, if any, is named \"<name>-data\" (volume names have at most 32 characters)."

  validation {
    condition     = length(var.name) >= 1 && length(var.name) <= 27
    error_message = "name must have 1 to 27 characters, so that the volume name \"<name>-data\" fits SimpleVM's 32-character limit."
  }
}

variable "project_id" {
  type        = number
  default     = null
  description = "SimpleVM project ID. Defaults to the provider's project_id."
}

variable "flavor" {
  type        = string
  description = "Flavor name, for example \"de.NBI default\"."
}

variable "image" {
  type        = string
  description = "Base image or snapshot image name."
}

variable "research_environment_template" {
  type        = string
  default     = null
  description = "Research environment template, for example \"rstudio\". Leave null for a plain VM."
}

variable "volume_size_gb" {
  type        = number
  default     = null
  description = "Size in GiB of a data volume to create and attach. Leave null for no volume."
}

variable "power_state" {
  type        = string
  default     = "active"
  description = "Desired power state of the VM: \"active\" or \"stopped\"."

  validation {
    condition     = contains(["active", "stopped"], var.power_state)
    error_message = "power_state must be \"active\" or \"stopped\"."
  }
}
