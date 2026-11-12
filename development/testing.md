# Testing

## Unit and fake-API tests

```bash
make test        # go test ./... -count=1
```

- `internal/client` tests run against `httptest` servers: auth header, pagination, endpoint normalisation, errors, polling.
- `internal/fakeapi` is a stateful fake of the SimpleVM API. Asynchronous operations advance one step per `GET`, so tests are deterministic and need no timers.
- `internal/provider` tests (`TestXxx`) use `terraform-plugin-testing` against the fake server. They need a Terraform or OpenTofu binary but no credentials and no network access to SimpleVM. Tests that wait use a very short `SIMPLEVM_POLL_INTERVAL`.

Lint with `make lint` (golangci-lint).

## Acceptance tests (real API)

Acceptance tests (`TestAccXxx`) create billable resources in a real SimpleVM project. They run only with `TF_ACC=1`.

```bash
export SIMPLEVM_API_KEY=...        # key with all scopes, "own objects only" is fine
export SIMPLEVM_PROJECT_ID=1234
make testacc
```

Optional: `SIMPLEVM_ACC_FLAVOR`, `SIMPLEVM_ACC_IMAGE` and `SIMPLEVM_ACC_RESENV_IMAGE` (the research environment test is skipped without it). Resources are named `tfacc-xxxxxx`. If a test is interrupted, delete leftovers in the portal; snapshots can only be deleted there.

In CI they run through the `acceptance.yml` workflow, on manual dispatch only (with a `cli` input; it skips with a notice when the secrets are not set), using the repository secrets `SIMPLEVM_API_KEY` and `SIMPLEVM_PROJECT_ID`. There is no schedule because every run of `TestAccVM_FullLifecycle` leaves behind a `tfacc-` snapshot that the API cannot delete; remove it in the portal after each run.

## Running against OpenTofu

By default `terraform-plugin-testing` uses the `terraform` binary on `PATH`. To use OpenTofu:

```bash
TF_ACC_TERRAFORM_PATH=$(which tofu) \
TF_ACC_PROVIDER_NAMESPACE=deNBI \
TF_ACC_PROVIDER_HOST=registry.opentofu.org \
go test ./... -count=1
```

(Add `TF_ACC=1` for the real-API tests.)

## Trying the provider by hand

Build the provider and point the CLI at it with `dev_overrides`. Put this in `~/.tofurc` (OpenTofu) or `~/.terraformrc` (Terraform), or in any file named by `TF_CLI_CONFIG_FILE`:

```hcl
provider_installation {
  dev_overrides {
    "deNBI/simplevm" = "/path/to/terraform-provider-simplevm" # directory with the built binary
  }
  direct {}
}
```

```bash
go build -o /path/to/terraform-provider-simplevm/ .
```

With `dev_overrides`, skip `init` for the provider (it warns that the provider is overridden; that is expected). `tofu validate`/`plan` work directly.

## Validating docs and examples

```bash
make docs
go tool tfplugindocs validate --provider-name simplevm
terraform fmt -check -recursive examples
terraform -chdir=examples/modules/research-vm validate   # needs dev_overrides
```
