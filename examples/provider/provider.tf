terraform {
  required_providers {
    simplevm = {
      source = "deNBI/simplevm"
    }
  }
}

# The API key is read from the SIMPLEVM_API_KEY environment variable and the
# project from SIMPLEVM_PROJECT_ID, so no secret ends up in the configuration.
provider "simplevm" {
  # endpoint   = "https://simplevm.denbi.de/portal/api/" # or SIMPLEVM_ENDPOINT
  # api_key    = var.simplevm_api_key                    # or SIMPLEVM_API_KEY
  # project_id = 1234                                    # or SIMPLEVM_PROJECT_ID
}
