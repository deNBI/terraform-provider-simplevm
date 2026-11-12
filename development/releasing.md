# Releasing

Releases are built by GoReleaser from a tag and signed with GPG, in the format both registries expect.

## One-time setup

Repository secrets (Settings, Secrets and variables, Actions):

| Secret | Used by | Content |
|--------|---------|---------|
| `GPG_PRIVATE_KEY` | `release.yml` | ASCII-armored private signing key |
| `PASSPHRASE` | `release.yml` | passphrase of that key |
| `SIMPLEVM_API_KEY`, `SIMPLEVM_PROJECT_ID` | `acceptance.yml` | test project credentials |

The public part of the GPG key is what you register with the registries.

## Cutting a release

1. Move the Unreleased entries in `CHANGELOG.md` under a new `## X.Y.Z - date` heading.
2. Make sure `make docs` leaves no changes, and CI is green on `main`. Run the acceptance tests (Actions, Acceptance, Run workflow).
3. Tag and push:

    ```bash
    git tag vX.Y.Z
    git push origin vX.Y.Z
    ```

4. `release.yml` runs GoReleaser: it builds the binaries, zips them, writes the checksums file, signs it and attaches `terraform-registry-manifest.json` to the GitHub release.
5. Check the release assets.

## Registry onboarding (first release only)

The repository must be named `deNBI/terraform-provider-simplevm`.

- **Terraform Registry** (registry.terraform.io): sign in with GitHub, choose Publish, Provider, select the repository, and add the public GPG key under the organization's signing keys. New versions are then picked up through the release webhook.
- **OpenTofu Registry** (opentofu.org/registry): submit the provider through the registry's GitHub issue form (see its documentation) and provide the public GPG key. New versions are picked up from GitHub releases.

After the first release, update the wiki page and README to drop the "not yet released" remarks, and copy `wiki/simple_vm/opentofu.md` to the SimpleVM wiki repository.
