# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses semantic versioning.

## Unreleased

### Added

- Provider `simplevm` with API key authentication (`X-API-KEY`) and configuration through `SIMPLEVM_ENDPOINT`, `SIMPLEVM_API_KEY` and `SIMPLEVM_PROJECT_ID`.
- Resources `simplevm_vm` (base image or research environment, `power_state`), `simplevm_volume`, `simplevm_volume_attachment` and `simplevm_snapshot`.
- Data sources `simplevm_project(s)`, `simplevm_flavor(s)`, `simplevm_image(s)`, `simplevm_snapshot_images`, `simplevm_vms` and `simplevm_volumes`.
- Generated reference documentation, a user guide for the SimpleVM wiki and the `research-vm` example module.

### Fixed

- `simplevm_vm` no longer fails with HTTP 403 while waiting for a VM: API keys may not use `GET /vms/`, so VMs are read with `GET /vms/{uuid}/` and listed with `GET /vms/by-api-key/` (also used by `simplevm_vms`).
- `simplevm_vm` no longer fails with "Provider produced inconsistent result after apply" (and does not plan a replacement on every refresh) because SimpleVM appends a suffix to the VM name. `name` keeps the configured value; the new computed attribute `full_name` holds the name SimpleVM reports.

### Known limitations

- Clusters (BiBiGrid) and member management are not supported.
- Snapshots cannot be deleted through the SimpleVM API; destroying a `simplevm_snapshot` only removes it from the state.
