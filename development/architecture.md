# Architecture

## Package layout

```
main.go                      provider server entrypoint (address registry.terraform.io/deNBI/simplevm)
internal/client/             hand-written SimpleVM REST client
  client.go                  Config, Client, request handling, APIError, IsNotFound
  list.go                    list decoding (bare array or DRF page) and pagination
  wait.go                    WaitFor polling helper
  types.go                   API structs
  projects.go vms.go volumes.go snapshots.go
internal/fakeapi/            stateful in-process fake of the SimpleVM API for tests
internal/provider/           terraform-plugin-framework provider
  provider.go helpers.go     configuration, project ID resolution, poll interval, diagnostics
  *_resource.go              simplevm_vm, _volume, _volume_attachment, _snapshot
  *_data_source.go           project(s), flavor(s), image(s), snapshot_images, vms, volumes
examples/ templates/ docs/   example HCL, docs templates, generated reference docs
wiki/simple_vm/opentofu.md   user guide for the SimpleVM wiki
```

The provider speaks protocol v6 and is built with `terraform-plugin-framework`.

## Client

- Base URL defaults to `https://simplevm.denbi.de/portal/api/`. The endpoint is normalised to end with `/`, and all request paths end with `/` as well, because Django redirects otherwise and drops the body of a POST. The endpoint must be `https`; plain `http` is allowed for loopback addresses only (tests).
- The API key is sent as `X-API-KEY: <key>`.
- List endpoints may return a DRF page (`count/next/previous/results`) or a bare array; both are decoded, pages are followed.
- HTTP 401/403 become errors that name the status and hint at the key scopes. They are never treated as "not found". Only 404 (`client.IsNotFound`) removes an object from state.
- There is no `GET /vms/{uuid}/`; `GetVM` scans the paginated `GET /vms/` list.

## Action to resource mapping

The API's `*/action/` endpoints are called from resource lifecycle methods, not exposed on their own.

| Resource | Operation | API call |
|----------|-----------|----------|
| `simplevm_vm` | create | `POST /vms/`, poll `GET /vms/` |
| | update `power_state` | `POST /vms/{id}/action/` with `os_action` `stop` / `resume` |
| | delete | `DELETE /vms/{id}/`, poll until gone |
| `simplevm_volume` | create | `POST /volumes/`, poll to `available` |
| | rename | `POST /volumes/{id}/action/` `rename` (`new_name`) |
| | grow | `POST /volumes/{id}/action/` `extend` (`size`) |
| | delete | `DELETE /volumes/{id}/` |
| `simplevm_volume_attachment` | create | volume action `attach` (`vm_uuid`), poll to `in-use` |
| | delete | volume action `detach`, poll to `available` |
| `simplevm_snapshot` | create | `POST /snapshots/`, poll `GET /projects/{id}/snapshots/images/` |
| | delete | none: state only, with a warning |

## Polling and state rules

- Operations are asynchronous. Resources call `client.WaitFor` with a poll interval (default `10s`, override with `SIMPLEVM_POLL_INTERVAL`, for development and tests only) until a terminal state or the timeout. Timeouts are attributes (`timeouts = { create = "45m" }`). Defaults: VM create 30m, VM update/delete 15m, volume and attachment operations 10m, snapshot create 60m.
- While polling, transient errors (`client.IsTransient`: HTTP 5xx, 429 and network errors, but not context cancellation or deadline expiry) are recorded as the observed status and polling continues; more than five in a row fail the wait with the last error. Other 4xx answers, including 401/403, fail at once. The client itself does not retry.
- A resource is saved to state as soon as the API returns its ID, before waiting, so a failed or timed-out wait leaves a tainted resource instead of an orphan. A create response without ID is an error and nothing is saved.
- VM create with a `research_environment` keeps polling until `vm_state` is `active`, `task_state` is none of `prepare_playbook_build`, `building_playbook`, `checking_ssh_connection`, and either every `playbook[].done` is true or `task_state` is `playbook_successful`; `playbook_failed` fails the create. If `power_state = "stopped"`, the VM is stopped only afterwards. If that stop fails, the create only warns: the VM is saved (not tainted) with the planned `power_state`, and the next refresh and plan show an in-place stop.
- A stop or resume is complete when `vm_state` is the target and `task_state` is not a transition state (`client.VMBusyTaskStates`, for example `powering-off`); idle task states such as `playbook_successful` do not block. An update that changes only `timeouts` sends no stop/resume.
- Read must not create drift from transient states: `power_state` is only taken from `vm_state` when that is `active` or `stopped`.
- API keys get HTTP 403 on `GET /vms/`. VMs are listed with `GET /vms/by-api-key/` (a bare array) and read one at a time with `GET /vms/{uuid}/` (404 when missing).
- SimpleVM appends a suffix to the requested VM name (`analysis` becomes `analysis-faaef`). `name` keeps the configured value while the reported name is that value plus such a suffix; the reported name is in `full_name`. An import sets `name` to the full name, and a config with the short name then updates `name` in place instead of replacing the VM.
- Read of an object that no longer exists removes it from state; the next plan proposes to create it.
- Snapshots cannot be read by ID or deleted. Create polls the snapshot images until the returned `openstack_id` (fallback: exact name) is `active`. Each time the snapshot is found in the snapshot-image list (in create or read), the provider private state key `seen` is set.
- Once `POST /snapshots/` returned an ID, a create that runs out of time is a warning, not an error: the snapshot is kept in state (not tainted) with its last observed `status`, or `pending` if it was never listed, because it may still complete and a re-create would leave an undeletable duplicate. A snapshot that enters a failure status (`error`, `killed`, `deleted`) still fails the create.
- Read removes a snapshot from state only when it is missing from the list **and** was `seen` before; a snapshot that was never listed is kept as is. This reduces the risk of duplicates, but does not rule them out: a snapshot whose create failed with an error is tainted, and the next apply creates a new one.
- Changes that alter what a VM or volume is use `RequiresReplace`. Only `power_state`, volume `name` and growing volume `size` update in place.

## Assumed request shapes

The SimpleVM OpenAPI schema does not document some request bodies. They are taken from the SimpleVM wiki and marked "assumed from wiki" in code comments:

- `POST /vms/` body: `project_id`, `image_name`, `flavor_name`, `vm_name`, optional `research_environment_backend { template, user_path, create_only }`.
- Volume action parameters `vm_uuid` (attach), `size` (extend), `new_name` (rename).
- `DELETE /volumes/{uuid}/`, `GET /projects/by-api-key/`.

The real-API acceptance tests exercise all of them: `TestAccVM_FullLifecycle` creates a VM, renames and grows a volume, attaches it and deletes both; `TestAccDataSources` reads `simplevm_project` without `id`. They had not yet been run against the real API when this was written. If the API disagrees, fix the client and the fake together.

## Adding a resource

1. Add the API structs and calls to `internal/client` (with an `httptest` unit test). Mark undocumented shapes "assumed from wiki".
2. Teach `internal/fakeapi` the new endpoints, including asynchronous states and 404s.
3. Create `internal/provider/<name>_resource.go` implementing `resource.Resource`, `ResourceWithConfigure` and, if possible, `ResourceWithImportState`. Give every attribute and the resource a `MarkdownDescription` (describe async behaviour and limitations). Use `RequiresReplace` for anything that cannot be updated in place and `timeouts` for anything that waits.
4. Register it in `Resources()` in `provider.go`.
5. Write fake-API tests (create, update, disappears out-of-band, import) and a `TestAcc...` real-API test.
6. Add `examples/resources/<type>/resource.tf` and, if importable, `import.sh`.
7. Run `make docs`, `make lint test`, and update `CHANGELOG.md` and the wiki page.
