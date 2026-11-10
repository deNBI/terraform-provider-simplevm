# SimpleVM OpenTofu/Terraform Provider — Design

Date: 2026-11-10
Status: Draft, awaiting approval (builder check done)

## Intent

de.NBI Cloud users currently click through the SimpleVM portal to create VMs,
pick a base image or research environment, attach volumes, start/stop machines
and take snapshots. We want those steps expressible as code so that a
`tofu apply` / `terraform apply` reproduces a project's SimpleVM setup, and so
the setup can be automated (CI, workshops prep, repeatable analysis envs).

Success: a user with a SimpleVM API key can write a short HCL file that creates
a VM from an image or research environment, attaches a volume, controls whether
it is running, snapshots it, and tears it all down again — with OpenTofu and
Terraform alike — and can learn how from a page in the SimpleVM wiki.

## Goals

- A Terraform/OpenTofu **provider** (`simplevm`) talking to the SimpleVM REST
  API at `https://simplevm.denbi.de/portal/api/`.
- Resources mirroring the API's resource objects; the API's `*Action`
  endpoints are invoked from resource lifecycle (create/update/delete), not
  exposed as standalone imperative resources.
- Data sources for lookups needed to fill resource arguments.
- Unit tests (mock HTTP server), acceptance tests (real API, opt-in), CI on
  GitHub Actions, release pipeline producing registry-compatible artifacts.
- User documentation written for the SimpleVM wiki; developer documentation
  in this repo.

## Non-goals (for now)

- Clusters / BiBiGrid. (Per partner: cluster creation is applying specific
  playbooks for a research environment; revisit when the API exposes it.)
- User / member management on VMs. The API key's owner becomes the VM owner;
  sharing access stays in the SimpleVM web UI.
- Workshops, port opening/UDP, monitoring, credits.
- One-off imperative VM actions (reboot, rescue/unrescue, enable/disable).
  These are not declarative state.

## Anti-goals

- Drift loops: a plan that never converges because the provider misreads API
  state (e.g. treating transient `building` as drift).
- Silently destroying user data: volume/VM replacement must be visible in the
  plan; nothing is replaced in-place without a `RequiresReplace` plan marker.
- Inventing undocumented API behaviour beyond what the OpenAPI schema and the
  wiki's API key page show, without flagging it in docs as an assumption.

## Constraints

- Public API surface (OpenAPI 3.0.3 at `/portal/api/schema/`, drf-spectacular):
  - `GET  /projects/`, `GET /projects/by-api-key/` (by-api-key: wiki only)
  - `GET  /projects/{id}/flavors/`, `/projects/{id}/images/`,
    `/projects/{id}/snapshots/images/` (paginated)
  - `GET  /vms/` (paginated), `GET /vms/by-api-key/` (wiki only), `POST /vms/`,
    `DELETE /vms/{uuid}/`, `POST /vms/{uuid}/action/` (`os_action`:
    stop, resume, reboot_soft, reboot_hard, rescue, unrescue, disable, enable)
  - `POST /volumes/`, `GET /volumes/{uuid}/`, `DELETE /volumes/{uuid}/` (wiki only),
    `GET /volumes/by-api-key/`, `POST /volumes/{uuid}/action/`
    (`os_action`: attach [vm_uuid], detach, extend [size], rename [new_name],
    clone, …), `GET /volumes/{uuid}/events/`
  - `POST /snapshots/` (name, description, vm_uuid). No read or delete.
- Auth: header `X-API-KEY: <key>` (wiki). The OpenAPI scheme says header
  `Authorization` with prefix `X-API-KEY`; we send `X-API-KEY` as the wiki
  shows. Keys are per project, scoped (`project:read`, `vm:read`,
  `volume:change_state`, …), and may be limited to "own objects only".
- `POST /vms/` request body is undocumented in the schema; the wiki shows
  `project_id`, `image_name`, `flavor_name`, `vm_name`, and optional
  `research_environment_backend { template, user_path, create_only }`.
  We send JSON.
- No `GET /vms/{uuid}/`: read is done by scanning the paginated `GET /vms/`
  list for the UUID.
- Operations are asynchronous: VM `vm_state` / `task_state` and volume
  `status` must be polled to a terminal state with configurable timeouts.
- Must work with OpenTofu ≥ 1.6 and Terraform ≥ 1.5 (protocol v6).
- License: AGPL-3.0 (existing LICENSE).

## Decided how

- **Provider, not an HCL module** (my call, stated to partner in first message,
  not objected to): an HCL module cannot call a custom REST API cleanly. An
  example *module* composing the provider's resources is shipped under
  `examples/modules/` for the "module" use-case.
- Go, HashiCorp **terraform-plugin-framework** (protocol v6), tfplugindocs for
  generated reference docs, GoReleaser for releases.
- API client in its own package (`internal/client`), hand-written, typed
  structs derived from the OpenAPI schema, with pagination, polling helpers and
  typed API errors (404 → resource removed from state).
- Scope as agreed: VMs incl. base image / research environment selection,
  start/stop, volumes, volume attachment, snapshots. Owner = API key owner.

### Provider configuration

```hcl
provider "simplevm" {
  endpoint = "https://simplevm.denbi.de/portal/api/" # env SIMPLEVM_ENDPOINT
  api_key  = var.simplevm_api_key                    # env SIMPLEVM_API_KEY, sensitive
  # project_id optional default for resources; env SIMPLEVM_PROJECT_ID
}
```

### Resources

`simplevm_vm`
- Args: `project_id` (optional if provider default; RequiresReplace),
  `name` (RequiresReplace), `flavor` (name; RequiresReplace), `image` (name of
  image or snapshot image; RequiresReplace), optional `research_environment {
  template, user_path, create_only }` (RequiresReplace),
  `power_state` (`active` | `stopped`, default `active`; updatable in place via
  stop/resume action), `timeouts`.
- Computed: `id` (= uuid), `openstack_id`, `vm_state`, `task_state`,
  `fixed_ip`, `research_environment_url`, `image_name`, `created_at`.
- Create: POST, then poll until `vm_state` is `active` (or failure state
  `error`/`creation_failed` → error). With a `research_environment`, keep
  polling until the playbook has finished (`task_state` no longer one of
  `prepare_playbook_build`/`building_playbook`/`checking_ssh_connection`, or
  `playbook[].done`); `playbook_failed` fails the create so the resource is
  tainted (my call). Only then, if `power_state = stopped`, stop it.
- Read: find in `/vms/` list; deleted/not found → remove from state.
  `power_state` is set from `vm_state` only when `vm_state` is `active` or
  `stopped` (other states don't cause drift).
- Delete: DELETE, poll until gone or `vm_state` in `deleted`/`soft-delete`/
  `marked_as_deletable`. Per partner: attached volumes are detached and kept
  (they return to `available`); deletion completes within minutes.
- Import by UUID.

`simplevm_volume`
- Args: `project_id` (RequiresReplace), `name` (update via `rename`),
  `size` GiB (grow via `extend`; shrink → RequiresReplace), optional
  `source_volume_id` / `volume_snapshot_id` (RequiresReplace).
- Computed: `id` (uuid), `openstack_id`, `status`, `device`, `path`.
- Create → poll to `available`. Delete → DELETE `/volumes/{uuid}/`.
- Import by UUID.

`simplevm_volume_attachment`
- Args: `volume_id`, `vm_id` (both RequiresReplace).
- Create: action `attach` with `vm_uuid`, poll volume to `in-use`.
- Read: volume's `virtual_machine` matches the VM; otherwise gone.
- Delete: action `detach`, poll to `available`.
- Import as `<volume_uuid>/<vm_uuid>`.

`simplevm_snapshot`
- Args: `vm_id`, `name`, `description` (all RequiresReplace).
- Computed: `openstack_id`, `status`, `created_at`.
- Create: POST `/snapshots/`, then poll `/projects/{id}/snapshots/images/`
  until an image with the returned `openstack_id` (fallback: exact name)
  appears and is `active` (my call — avoids duplicate snapshots from a
  "missing → recreate" loop, since snapshots can't be deleted via API).
- Read: look up by `openstack_id`; removed from state only when it was seen
  before and is now absent.
- Delete: **no API endpoint** — removes from state only and emits a warning
  that the image must be deleted in the SimpleVM UI. Documented.

### Data sources

- `simplevm_project` (by id or the key's project via `by-api-key`)
- `simplevm_projects`
- `simplevm_flavors` / `simplevm_flavor` (by name) for a project
- `simplevm_images` / `simplevm_image` (by name, optional tag filter;
  exposes `resenv_template_name` so research environments can be picked)
- `simplevm_snapshot_images`
- `simplevm_vms`, `simplevm_volumes` (by-api-key listing)

### Testing

- Unit tests for the client against `httptest` servers (auth header,
  pagination, errors, polling).
- Provider resource tests using `terraform-plugin-testing` against an
  in-process fake SimpleVM server (stateful mock) — run in CI without
  credentials, across Terraform and OpenTofu CLIs.
- Acceptance tests against the real API, gated by `TF_ACC=1` and
  `SIMPLEVM_API_KEY`/`SIMPLEVM_PROJECT_ID`; run in CI only on manual dispatch
  / when secrets are present.

Per partner: a test project + API key will be provided as repo secrets
(`SIMPLEVM_API_KEY`, `SIMPLEVM_PROJECT_ID`); acceptance tests may create and
destroy real VMs, volumes and snapshots, and are used to verify the request
shapes the schema doesn't document (`POST /vms/` body, volume action params).

### Publishing (per partner)

- Published to **both** registry.terraform.io and the OpenTofu registry.
- Repo will be renamed (by partner) to `deNBI/terraform-provider-simplevm`;
  Go module path `github.com/deNBI/terraform-provider-simplevm`; users write
  `source = "deNBI/simplevm"`.
- Releases GPG-signed via GoReleaser; signing key + passphrase as repo secrets
  (`GPG_PRIVATE_KEY`, `PASSPHRASE`), registry manifest included.

### CI / Release

- GitHub Actions: `test.yml` (gofmt/vet, golangci-lint, unit+mock tests,
  matrix over Terraform & OpenTofu versions, docs-generated check),
  `acceptance.yml` (workflow_dispatch, uses repo secrets),
  `release.yml` (tag `v*` → GoReleaser with GPG signing, registry manifest).

### Documentation

- Generated reference docs in `docs/` (tfplugindocs format: `docs/index.md`,
  `docs/resources/*.md`, `docs/data-sources/*.md`) from schema descriptions +
  `templates/` + `examples/`.
- `wiki/simple_vm/opentofu.md`: user-facing page written in the SimpleVM wiki's
  MkDocs style (prerequisites: API key with scopes; install; quickstart; recipes
  for research env, volumes, start/stop, snapshot; limits & FAQ), ready to copy
  into the wiki repo.
- `CONTRIBUTING.md` / `docs/development.md`: architecture, local build,
  dev_overrides for tofu/terraform, running tests, adding a resource,
  regenerating docs, releasing.

## Left to the builder

- Exact polling intervals/back-off; default timeouts (suggested 30m create VM,
  10m volume ops).
- Internal package layout below `internal/`.
- Field naming details where they don't change behaviour.
- Which mock-server fidelity is enough, as long as lifecycle (async states,
  404s, pagination) is exercised.
