# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses semantic versioning.

## Unreleased

### Added

- Provider `simplevm` with API key authentication (`X-API-KEY`) and configuration through `SIMPLEVM_ENDPOINT`, `SIMPLEVM_API_KEY` and `SIMPLEVM_PROJECT_ID`.
- Resources `simplevm_vm` (base image or research environment, `power_state`), `simplevm_volume`, `simplevm_volume_attachment` and `simplevm_snapshot`.
- Data sources `simplevm_project(s)`, `simplevm_flavor(s)`, `simplevm_image(s)`, `simplevm_snapshot_images`, `simplevm_vms` and `simplevm_volumes`.
- Generated reference documentation, a user guide for the SimpleVM wiki and the `research-vm` example module.
- Computed attributes `ssh_host`, `ssh_port` and `ssh_user` on `simplevm_vm` and on the VMs of `simplevm_vms`, from the VM's SSH access mode in SimpleVM (usually the site's SSH gateway and a port per VM). They can be used in `connection` blocks and inventories. Hosts and users that ssh could read as an option are rejected, and the attributes are null then.

### Fixed

- `simplevm_vm` no longer fails with HTTP 403 while waiting for a VM: API keys may not use `GET /vms/`, so VMs are read with `GET /vms/{uuid}/` and listed with `GET /vms/by-api-key/` (also used by `simplevm_vms`).
- `simplevm_vm` no longer fails with "Provider produced inconsistent result after apply" (and does not plan a replacement on every refresh) because SimpleVM appends a suffix to the VM name. `name` keeps the configured value; the new computed attribute `full_name` holds the name SimpleVM reports.
- A single transient error (HTTP 5xx, 429, network error) of the SimpleVM API no longer fails plan, apply or destroy outside of polling: GET and DELETE requests are sent up to 4 times in total. The SimpleVM gateway occasionally answers `504 Gateway Time-out` or stalls. POST requests are not retried. Retries back off exponentially with jitter (from 1s, at most 30s), honour `Retry-After` up to one minute, and a transient error pauses all requests of the provider, so parallel operations do not pile onto a struggling server. `SIMPLEVM_RETRY_WAIT` overrides the base wait for development and testing.

### Known limitations

- Clusters (BiBiGrid) and member management are not supported.
- Snapshots cannot be deleted through the SimpleVM API; destroying a `simplevm_snapshot` only removes it from the state.
