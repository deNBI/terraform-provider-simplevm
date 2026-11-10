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

- Operations are asynchronous. Resources call `client.WaitFor` with a poll interval (default `10s`, override with `SIMPLEVM_POLL_INTERVAL`, for development and tests only) until a terminal state or the timeout. Timeouts are attributes (`timeouts = { create = "45m" }`). Defaults: VM create 30m, VM update/delete 15m, volume, attachment and snapshot operations 10m.
- A resource is saved to state as soon as the API returns its ID, before waiting, so a failed or timed-out wait leaves a tainted resource instead of an orphan.
- VM create with a `research_environment` keeps polling until the playbook has finished; `playbook_failed` fails the create. If `power_state = "stopped"`, the VM is stopped only afterwards.
- Read must not create drift from transient states: `power_state` is only taken from `vm_state` when that is `active` or `stopped`.
- Read of an object that no longer exists removes it from state; the next plan proposes to create it.
- Snapshots cannot be listed by ID or deleted. Create polls the snapshot images until the returned `openstack_id` (fallback: exact name) is `active`. Because create only returns once the image is listed and `active`, the snapshot has been seen once by then. Read removes the resource from state whenever its ID is no longer in the project's snapshot-image list. Waiting in create (rather than treating a missing snapshot as "to be created") is what avoids duplicate, undeletable snapshots; there is no further protection, and a create that times out is tainted, so users must check the portal before re-applying.
- Changes that alter what a VM or volume is use `RequiresReplace`. Only `power_state`, volume `name` and growing volume `size` update in place.

## Assumed request shapes

The SimpleVM OpenAPI schema does not document some request bodies. They are taken from the SimpleVM wiki and marked "assumed from wiki" in code comments:

- `POST /vms/` body: `project_id`, `image_name`, `flavor_name`, `vm_name`, optional `research_environment_backend { template, user_path, create_only }`.
- Volume action parameters `vm_uuid` (attach), `size` (extend), `new_name` (rename).
- `DELETE /volumes/{uuid}/`, `GET /projects/by-api-key/`, `/vms/by-api-key/`.

The real-API acceptance tests exercise these shapes, but had not yet been run against the real API when this was written. If the API disagrees, fix the client and the fake together.

## Adding a resource

1. Add the API structs and calls to `internal/client` (with an `httptest` unit test). Mark undocumented shapes "assumed from wiki".
2. Teach `internal/fakeapi` the new endpoints, including asynchronous states and 404s.
3. Create `internal/provider/<name>_resource.go` implementing `resource.Resource`, `ResourceWithConfigure` and, if possible, `ResourceWithImportState`. Give every attribute and the resource a `MarkdownDescription` (describe async behaviour and limitations). Use `RequiresReplace` for anything that cannot be updated in place and `timeouts` for anything that waits.
4. Register it in `Resources()` in `provider.go`.
5. Write fake-API tests (create, update, disappears out-of-band, import) and a `TestAcc...` real-API test.
6. Add `examples/resources/<type>/resource.tf` and, if importable, `import.sh`.
7. Run `make docs`, `make lint test`, and update `CHANGELOG.md` and the wiki page.
