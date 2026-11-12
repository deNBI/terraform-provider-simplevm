# SimpleVM Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Terraform/OpenTofu provider `deNBI/simplevm` that manages SimpleVM VMs (base image or research environment, power state), volumes, volume attachments and snapshots, with data sources, tests, CI, release pipeline, wiki user docs and developer docs.

**Architecture:** Hand-written typed REST client in `internal/client` (auth, pagination, polling); provider, resources and data sources in `internal/provider` on terraform-plugin-framework (protocol v6); an in-process stateful fake SimpleVM API in `internal/fakeapi` so resource lifecycle tests run in CI without credentials; real-API acceptance tests gated by `TF_ACC` + secrets.

**Tech Stack:** Go ≥ 1.24, terraform-plugin-framework v1.19.0, terraform-plugin-framework-timeouts v0.7.0, terraform-plugin-testing v1.16.0, terraform-plugin-log v0.11.0, terraform-plugin-docs v0.25.0, GoReleaser v2, golangci-lint v2, GitHub Actions.

**Spec:** `development/superpowers/specs/2026-11-10-simplevm-provider-design.md`

## Global Constraints

- Go module path: `github.com/deNBI/terraform-provider-simplevm`; provider address `registry.terraform.io/deNBI/simplevm`; type name `simplevm`.
- Plugin protocol v6; must work with Terraform ≥ 1.5 and OpenTofu ≥ 1.6.
- Default endpoint `https://simplevm.denbi.de/portal/api/`; all request paths end with `/` (Django).
- Auth header: `X-API-KEY: <key>` (not `Authorization`).
- Provider env vars: `SIMPLEVM_ENDPOINT`, `SIMPLEVM_API_KEY`, `SIMPLEVM_PROJECT_ID`; dev-only `SIMPLEVM_POLL_INTERVAL` (Go duration, default `10s`).
- Acceptance secrets: `SIMPLEVM_API_KEY`, `SIMPLEVM_PROJECT_ID`; release secrets: `GPG_PRIVATE_KEY`, `PASSPHRASE`.
- Default timeouts: VM create 30m, VM update/delete 15m; volume/attachment/snapshot operations 10m.
- Out of scope: clusters/BiBiGrid, member management, workshops, reboot/rescue/enable/disable.
- Anything changing what a VM/volume *is* uses `RequiresReplace`; only `power_state`, volume `name` and volume `size` (grow) update in place.
- Request shapes not documented in the OpenAPI schema (`POST /vms/` body, volume action params `vm_uuid`/`size`/`new_name`) are marked "assumed from wiki" in code comments and docs.
- License AGPL-3.0 (existing `LICENSE`). Developer/process docs live under `development/`, never under `docs/` (tfplugindocs-managed; `tfplugindocs validate` rejects unknown dirs).

## Review Focus

1. A list endpoint (esp. `*/by-api-key/`) returning a bare JSON array instead of a DRF page `{count,next,previous,results}` → both decode. (Task 2 test `TestDecodeList_ArrayAndPage`)
2. Invalid key or missing scope (HTTP 401/403) → error naming the status and hinting at key scopes; never treated as "not found" / removed from state. (Task 2 `TestDo_AuthErrors`, Task 6 `TestVM_ForbiddenIsError`)
3. `endpoint` configured without trailing slash, or with a path like `/portal/api` → requests still go to `.../portal/api/vms/` (POST must not be redirected, Django drops the body). (Task 2 `TestNew_EndpointNormalisation`)
4. VM in a transient state (`building`, `rebooting`, `task_state` set) during refresh with `power_state = "active"` → no diff, no spurious stop/resume. (Task 6 `TestVM_TransientStateNoDrift`)
5. Object deleted out-of-band in the SimpleVM UI → refresh removes it from state, plan proposes create, no error. (Task 6 `TestVM_Disappears`, Task 7 `TestVolume_Disappears`)

---

## File Structure

```
main.go                                   provider server entrypoint
go.mod / go.sum
GNUmakefile                               build, test, testacc, docs, lint targets
.golangci.yml  .goreleaser.yml  terraform-registry-manifest.json  .gitignore
internal/client/
  client.go        Config, Client, New, do(), APIError, IsNotFound
  list.go          decodeList (array or DRF page), listAll pagination
  wait.go          WaitFor polling helper
  types.go         API structs (Project, Flavor, Image, VM, Volume, Snapshot, ...)
  projects.go      projects, flavors, images, snapshot images
  vms.go           VM CRUD + action
  volumes.go       volume CRUD + action
  snapshots.go     snapshot create
  *_test.go
internal/fakeapi/
  server.go        stateful fake SimpleVM API (httptest), state machines
  server_test.go
internal/provider/
  provider.go                     provider, config, ProviderData
  provider_test.go                factories, fake server harness, preCheck helpers
  helpers.go                      projectID resolution, poll interval, diag helpers
  vm_resource.go                  simplevm_vm
  volume_resource.go              simplevm_volume
  volume_attachment_resource.go   simplevm_volume_attachment
  snapshot_resource.go            simplevm_snapshot
  project_data_source.go          simplevm_project, simplevm_projects
  flavor_data_source.go           simplevm_flavor, simplevm_flavors
  image_data_source.go            simplevm_image, simplevm_images, simplevm_snapshot_images
  inventory_data_source.go        simplevm_vms, simplevm_volumes
  *_test.go                       fake-server tests (TestXxx) + real API (TestAccXxx)
examples/provider/provider.tf
examples/resources/<type>/resource.tf, import.sh
examples/data-sources/<type>/data-source.tf
examples/modules/research-vm/{main.tf,variables.tf,outputs.tf,versions.tf,README.md}
templates/index.md.tmpl
docs/                                     generated by tfplugindocs (do not hand-edit)
wiki/simple_vm/opentofu.md                user page for the SimpleVM wiki (MkDocs)
README.md  CONTRIBUTING.md  CHANGELOG.md
development/architecture.md  development/testing.md  development/releasing.md
.github/workflows/{test.yml,acceptance.yml,release.yml}
.github/dependabot.yml
```

---

### Task 1: Scaffold module and provider skeleton

**Files:**
- Create: `go.mod`, `main.go`, `GNUmakefile`, `.gitignore`, `.golangci.yml`, `internal/provider/provider.go`, `internal/provider/provider_test.go`

**Interfaces:**
- Produces:
  - `provider.New(version string) func() provider.Provider`
  - Task 1's `Configure` only resolves and validates config values; `ProviderData` is introduced in Task 2 Step 4 when the client exists.
  - Test helper `testProtoV6ProviderFactories map[string]func() (tfprotov6.ProviderServer, error)` keyed `"simplevm"`.
- `main.go`: `providerserver.Serve` with address `registry.terraform.io/deNBI/simplevm`, `-debug` flag, `version` var set by ldflags.

- [ ] **Step 1: `go mod init github.com/deNBI/terraform-provider-simplevm`, add framework/testing/log deps at the Tech Stack versions.**
- [ ] **Step 2: Write failing test** `TestProvider_SchemaValid` in `provider_test.go`: call `New("test")().Schema(...)`, then `resp.Schema.ValidateImplementation(ctx)` has no errors; assert attributes `endpoint` (optional string), `api_key` (optional, sensitive), `project_id` (optional int64) exist.
- [ ] **Step 3: Run** `go test ./internal/provider/ -run TestProvider_SchemaValid` → FAIL (package missing).
- [ ] **Step 4: Implement** provider `Metadata` (TypeName `simplevm`, Version), `Schema`, `Configure`: values from config, else env vars; `api_key` missing → error diagnostic "api_key must be set or SIMPLEVM_API_KEY exported"; unknown values during plan → error asking for known values. Put `ProviderData` into `resp.ResourceData` / `resp.DataSourceData`.
- [ ] **Step 5: Run test → PASS; `go vet ./...` clean; `go build ./...` ok.**
- [ ] **Step 6: Makefile targets** `build`, `install`, `test` (`go test ./... -count=1`), `testacc` (`TF_ACC=1 go test ./... -run TestAcc -timeout 120m`), `lint` (`golangci-lint run`), `docs` (`go generate ./...`), `fmt`. Add `//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name simplevm` in `main.go` and `tools/tools.go` (build tag `tools`) importing tfplugindocs.
- [ ] **Step 7: Commit** `feat: scaffold simplevm provider`.

### Task 2: Client core — config, requests, errors, list decoding, polling

**Files:**
- Create: `internal/client/client.go`, `list.go`, `wait.go`, `client_test.go`, `list_test.go`, `wait_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { Endpoint, APIKey, UserAgent string; HTTPClient *http.Client }`
  - `func New(cfg Config) (*Client, error)` — empty Endpoint → default; ensures trailing `/`; errors on empty APIKey or unparsable URL.
  - `func (c *Client) do(ctx context.Context, method, path string, body any, out any) error` — `path` relative, no leading slash (e.g. `"vms/"`); JSON request body; sets `X-API-KEY`, `Accept: application/json`, `User-Agent`; logs via `tflog` with the key masked.
  - `type APIError struct { StatusCode int; Method, URL, Body string }` with `Error()`; for 401/403 message includes `"check that the API key is valid, not expired and has the required scope"`.
  - `func IsNotFound(err error) bool` (true for `*APIError` 404 and for `ErrNotFound`); `var ErrNotFound = errors.New("not found")`.
  - `func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error)` — follows DRF `next` (absolute URL, request it as-is), `page_size=100` on the first request.
  - `func decodeList[T any](data []byte) (items []T, next string, err error)` — accepts JSON array or `{"results":[...],"next":...}`.
  - `func WaitFor(ctx context.Context, interval time.Duration, check func(ctx context.Context) (done bool, status string, err error)) error` — returns `check` errors immediately; on ctx deadline returns `fmt.Errorf("timed out waiting; last observed status %q: %w", status, ctx.Err())`.

- [ ] **Step 1: Write failing tests**
  - `TestNew_EndpointNormalisation`: for `"https://x/portal/api"`, `"https://x/portal/api/"`, `""` (→ default) a GET `vms/` hits path `/portal/api/vms/` (use httptest for the first two; for `""` assert `c.baseURL.String() == "https://simplevm.denbi.de/portal/api/"`).
  - `TestNew_RequiresAPIKey`: `New(Config{})` returns error containing `"api key"`.
  - `TestDo_SetsHeaders`: server asserts `X-API-KEY: secret`, `Content-Type: application/json` on POST, decodes body `{"a":1}`.
  - `TestDo_AuthErrors`: 401 and 403 → `*APIError` with StatusCode set, `IsNotFound` false, message contains `"scope"`.
  - `TestDo_NotFound`: 404 → `IsNotFound(err)` true.
  - `TestDecodeList_ArrayAndPage`: `[{"id":1}]` and `{"count":1,"next":null,"results":[{"id":1}]}` both yield one item; page with `next` returns it.
  - `TestListAll_FollowsNext`: two pages (3 + 2 items) → 5 items, second request is to the `next` URL.
  - `TestWaitFor_DoneAfterN`, `TestWaitFor_ErrorStops`, `TestWaitFor_TimeoutReportsLastStatus` (ctx 50ms, interval 10ms, status `"building"` → error contains `building`).
- [ ] **Step 2: Run** `go test ./internal/client/` → FAIL.
- [ ] **Step 3: Implement** the interfaces above. Response bodies on error truncated to 2 KiB.
- [ ] **Step 4: Run → PASS.** Then add to `internal/provider/provider.go` `type ProviderData struct { Client *client.Client; DefaultProjectID int64 /* 0 = unset */; PollInterval time.Duration }`, build it in `Configure` via `client.New` (UserAgent `terraform-provider-simplevm/<version>`), and re-run `go test ./...`.
- [ ] **Step 5: Commit** `feat(client): add API client core with pagination and polling`.

### Task 3: Client endpoints and types

**Files:**
- Create: `internal/client/types.go`, `projects.go`, `vms.go`, `volumes.go`, `snapshots.go`, `endpoints_test.go`

**Interfaces:**
- Produces (types; JSON tags = OpenAPI field names; nullable → pointer):
  - `Project{ID int64 "id"; Shortname, Description string; ClusterActivated bool "cluster_activated"; Disabled bool; VMsLimit, CoresLimit, RAMLimit, VolumeCounterLimit, VolumeStorageLimit int64; IsAdmin, IsMember bool}`
  - `Flavor{ID int64; Name string; VCPUs int64 "vcpus"; RAM int64 "ram"; RootDisk int64 "root_disk"; EphemeralDisk int64 "ephemeral_disk"; GPU int64 "gpu"; CreditsCostPerHour float64 "credits_cost_per_hour"}`
  - `Image{OpenstackID string "openstack_id"; Name, Title, Description, Status, OSDistro "os_distro", OSVersion "os_version" string; Tags []string; MinRAM "min_ram", MinDisk "min_disk" int64; IsSnapshot bool "is_snapshot"; ResenvTemplateName string "resenv_template_name"; CreatedAt string "created_at"}`
  - `VM{UUID, Name string; OpenstackID *string; VMState string "vm_state"; TaskState *string "task_state"; FixedIP string "fixed_ip"; ResearchEnvironmentURL string "research_environment_url"; ImageName string "image_name"; Project *int64 "project"; Flavor Flavor; Playbook Playbooks "playbook"; CreatedAtDate *string "created_at_date"}`
  - `type Playbooks []Playbook` with `UnmarshalJSON` accepting null, a single object, or an array; `Playbook{ID int64; Done bool}`; method `(p Playbooks) AllDone() bool` (true when empty).
  - `Volume{UUID, Name, Device, Path, Status string; Storage int64; OpenstackID *string; ProjectID json.Number "project_id"; VirtualMachine *VolumeMachine "virtual_machine"}`; `VolumeMachine{Name string; OpenstackID *string "openstack_id"; VMState string "vm_state"}`
  - `Snapshot{Name, OpenstackID "openstack_id", Status, Description, CreatedAt "created_at" string; ProjectID int64 "project_id"}`
  - Requests: `CreateVMRequest{ProjectID int64 "project_id"; ImageName "image_name", FlavorName "flavor_name", VMName "vm_name" string; ResearchEnvironment *ResearchEnvironmentBackend "research_environment_backend,omitempty"}`, `ResearchEnvironmentBackend{CreateOnly bool "create_only"; Template string "template"; UserPath string "user_path"}`, `CreateVolumeRequest{ProjectID int64 "project_id"; Storage int64 "storage"; Name string; SourceVolumeID "source_volume_id,omitempty", VolumeSnapID "volume_snap_id,omitempty" string}`, `VolumeActionRequest{OSAction "os_action", VMUUID "vm_uuid,omitempty", NewName "new_name,omitempty" string; Size int64 "size,omitempty"}`, `CreateSnapshotRequest{Name, Description string; VMUUID string "vm_uuid"}`
  - Responses: `VMActionResponse{UUID, OpenstackID, OSAction "os_action", VMState "vm_state", TaskState "task_state" string}`, `VolumeActionResponse{UUID, OpenstackID, OSAction, VolumeStatus "volume_status", Name string; Size int64}`
  - Methods on `*Client` (all `ctx context.Context` first):
    `ListProjects() ([]Project, error)` → `projects/`; `GetProjectByAPIKey() (*Project, error)` → `projects/by-api-key/` (accept object or single-element list); `GetProject(id int64)` (filter ListProjects; ErrNotFound);
    `ListFlavors(projectID int64) ([]Flavor, error)` → `projects/{id}/flavors/`; `ListImages(projectID)` → `projects/{id}/images/`; `ListSnapshotImages(projectID)` → `projects/{id}/snapshots/images/`;
    `ListVMs() ([]VM, error)` → `vms/`; `GetVM(uuid string) (*VM, error)` (scan ListVMs; ErrNotFound; also ErrNotFound when `vm_state` is `deleted`); `CreateVM(CreateVMRequest) (*VMActionResponse, error)`; `DeleteVM(uuid) error`; `VMAction(uuid, action string) (*VMActionResponse, error)` → `vms/{uuid}/action/` body `{"os_action":action}`;
    `ListVolumes() ([]Volume, error)` → `volumes/by-api-key/`; `GetVolume(uuid) (*Volume, error)` (ErrNotFound when status `deleted`); `CreateVolume(CreateVolumeRequest) (*VolumeActionResponse, error)`; `DeleteVolume(uuid) error`; `VolumeAction(uuid string, req VolumeActionRequest) (*Volume, error)`;
    `CreateSnapshot(CreateSnapshotRequest) (*Snapshot, error)` → `snapshots/`.
  - Constants: `VMAction{Stop="stop", Resume="resume"}`, VM states `VMStateActive="active"`, `VMStateStopped="stopped"`, `VMStateBuilding="building"`, failure set `VMFailureStates = []string{"error","creation_failed"}`, gone set `VMGoneStates = []string{"deleted","soft-delete","marked_as_deletable"}`, playbook task states `PlaybookRunningTaskStates = []string{"prepare_playbook_build","building_playbook","checking_ssh_connection"}`, `TaskStatePlaybookFailed = "playbook_failed"`; volume statuses `available`, `in-use`, failure set `error`, `creation_failed`, `error_extending`.

- [ ] **Step 1: Write failing table test** `TestEndpoints` in `endpoints_test.go`: for each method, an httptest handler asserts method + path + JSON body and returns a canned response (copy field names from the OpenAPI schema); assert decoded values. Include `TestPlaybooks_Unmarshal` (null / object / array) and `TestGetVM_NotFoundAndDeleted`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** Comment `// Request shape assumed from the SimpleVM wiki (not in OpenAPI schema).` on `CreateVMRequest` and `VolumeActionRequest`.
- [ ] **Step 4: Run** `go test ./internal/client/` → PASS.
- [ ] **Step 5: Commit** `feat(client): add projects, VM, volume and snapshot endpoints`.

### Task 4: Fake SimpleVM API

**Files:**
- Create: `internal/fakeapi/server.go`, `internal/fakeapi/server_test.go`

**Interfaces:**
- Consumes: `client` types (reuse them for JSON shapes).
- Produces:
  - `func New(t testing.TB, opts ...Option) *Server` (starts `httptest.Server`, `t.Cleanup` closes); fields/methods: `URL() string` (ends with `/portal/api/`), `APIKey = "test-key"`, `ProjectID int64 = 1`.
  - Seeded data: flavors `de.NBI default` and `de.NBI small`; images `Ubuntu-22.04` (no template) and `RStudio-Ubuntu-22.04` (`resenv_template_name: "rstudio"`).
  - State machines advance one step per GET that observes the object: VM `planned → building → active`; with research env additionally task_state `prepare_playbook_build → building_playbook → ""` and playbook `done=true`; stop: `active → (task powering-off) → stopped`; resume reverse; delete: `→ deleted` then absent. Volume `creating → available`; attach `attaching → in-use` (sets `virtual_machine`), detach `detaching → available`; extend `extending → available`. Snapshot images: appear on creation with status `queued`, then `active`.
  - Options: `WithPlaybookFailure()` (research env ends with task_state `playbook_failed`), `WithForbidden()` (every request 403).
  - Test hooks: `DeleteVMOutOfBand(uuid)`, `DeleteVolumeOutOfBand(uuid)`, `SetVMState(uuid, vmState, taskState string)`, `Requests() []RecordedRequest{Method, Path string; Body []byte}`.
  - Rejects requests without `X-API-KEY: test-key` (401) and paths without trailing slash (301, to catch bugs).

- [ ] **Step 1: Write failing tests** using the real `client` against the fake: `TestFake_VMLifecycle` (create → WaitFor active → stop → stopped → delete → GetVM IsNotFound), `TestFake_VolumeAttach`, `TestFake_Auth` (wrong key → 401).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** (mutex-protected maps). **Step 4: Run** `go test ./internal/fakeapi/` → PASS.
- [ ] **Step 5: Commit** `test: add stateful fake SimpleVM API`.

### Task 5: Data sources

**Files:**
- Create: `internal/provider/helpers.go`, `project_data_source.go`, `flavor_data_source.go`, `image_data_source.go`, `inventory_data_source.go`, `data_sources_test.go`; examples under `examples/data-sources/<type>/data-source.tf` for each.
- Modify: `internal/provider/provider.go` (register `DataSources`), `provider_test.go` (harness).

**Interfaces:**
- Produces in `helpers.go`: `func resolveProjectID(attr types.Int64, pd *ProviderData) (int64, diag.Diagnostics)` (attribute → provider default → error "project_id must be set on the resource or the provider"); `func providerData(raw any) (*ProviderData, diag.Diagnostics)`; poll interval from `SIMPLEVM_POLL_INTERVAL` parsed in `Configure` (default 10s).
- Produces in `provider_test.go`: `func fakeProviderConfig(s *fakeapi.Server) string` returning a `provider "simplevm" { endpoint = ..., api_key = "test-key", project_id = 1 }` block; `t.Setenv("SIMPLEVM_POLL_INTERVAL","10ms")` in a helper `newFake(t)`.
- Data sources (all read-only, `id` computed):
  - `simplevm_project` (`id` optional int64; absent → `GetProjectByAPIKey`): shortname, description, cluster_activated, disabled, limits.
  - `simplevm_projects`: `projects` list.
  - `simplevm_flavor` (`project_id` opt, `name` required): vcpus, ram, root_disk, ephemeral_disk, gpu, credits_cost_per_hour. Not found → error listing available names.
  - `simplevm_flavors` (`project_id` opt): `flavors` list.
  - `simplevm_image` (`project_id` opt, `name` required, `include_snapshots` bool default false): openstack_id, title, os_distro, os_version, tags, min_ram, min_disk, is_snapshot, resenv_template_name. Not found → error listing names.
  - `simplevm_images` (`project_id` opt, optional `tag` filter), `simplevm_snapshot_images` (`project_id` opt).
  - `simplevm_vms`, `simplevm_volumes`: lists with uuid, name, state/status.

- [ ] **Step 1: Write failing tests** (`resource.UnitTest` + fake): `TestDataSourceProject_ByAPIKey`, `TestDataSourceFlavor_ByName` (`vcpus` set), `TestDataSourceFlavor_UnknownNameListsAvailable` (`ExpectError: regexp.MustCompile("de.NBI default")`), `TestDataSourceImage_ResearchEnvTemplate` (`resenv_template_name = "rstudio"`), `TestDataSourceImages_TagFilter`, `TestDataSourceVMsAndVolumes_Empty`.
- [ ] **Step 2: Run** `go test ./internal/provider/ -run TestDataSource` → FAIL. **Step 3: Implement.** **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat: add project, flavor, image and inventory data sources`.

### Task 6: `simplevm_vm` resource

**Files:**
- Create: `internal/provider/vm_resource.go`, `vm_resource_test.go`, `examples/resources/simplevm_vm/{resource.tf,import.sh}`
- Modify: `provider.go` (register)

**Interfaces:**
- Consumes: client VM methods/constants (Task 3), `resolveProjectID`, `WaitFor`.
- Schema: `id` (computed uuid, UseStateForUnknown), `project_id` (opt+computed int64, RequiresReplace), `name`, `flavor`, `image` (required strings, RequiresReplace), `research_environment` (SingleNestedAttribute optional, RequiresReplace: `template` required, `user_path` optional default `""`, `create_only` optional bool default `false`), `power_state` (optional+computed, default `"active"`, validator OneOf `active`,`stopped`), `timeouts` (create/update/delete via terraform-plugin-framework-timeouts), computed: `openstack_id`, `vm_state`, `task_state`, `fixed_ip`, `research_environment_url`, `created_at`.
- Behaviour:
  - Create: `CreateVM`, `id` = response uuid (save partial state immediately so a failed wait leaves a tainted, deletable resource); WaitFor: done when `vm_state=active` and (no research env, or task_state not in `PlaybookRunningTaskStates` and `Playbook.AllDone()`); `vm_state` in failure states or task_state `playbook_failed` → error. Then if `power_state="stopped"`: `VMAction(stop)` + wait `stopped`.
  - Read: `GetVM`; `IsNotFound` → `RemoveResource`. Other errors (incl. 403) → diagnostic. Set `name`, computed fields; set `power_state` only when `vm_state` ∈ {active, stopped}; set `flavor` (`vm.Flavor.Name`), `image` (`vm.ImageName`), `project_id` only when the state value is null (import).
  - Update: only `power_state` changes → stop/resume + wait.
  - Delete: `DeleteVM` (404 tolerated) + wait until `GetVM` IsNotFound or vm_state ∈ `VMGoneStates`.
  - ImportState: passthrough `id`.

- [ ] **Step 1: Write failing tests** (fake):
  - `TestVM_BaseImage` — create, check `vm_state=active`, `power_state=active`, `openstack_id` set; step 2 `power_state="stopped"` → `vm_state=stopped` with `plancheck.ExpectResourceAction(..., plancheck.ResourceActionUpdate)`; step 3 back to active; step 4 `ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"timeouts","research_environment"}`.
  - `TestVM_ResearchEnvironmentWaitsForPlaybook` — assert in Check that fake's VM has playbook done and create request body contained `"research_environment_backend":{"create_only":false,"template":"rstudio","user_path":""}`.
  - `TestVM_PlaybookFailed` — `WithPlaybookFailure()` → `ExpectError: regexp.MustCompile("playbook_failed")`.
  - `TestVM_CreateStopped` — `power_state="stopped"` at create → `vm_state=stopped`.
  - `TestVM_TransientStateNoDrift` — after create, `SetVMState(uuid,"active","rebooting")` / `"building"`, then `PlanOnly: true` step with `ExpectNonEmptyPlan: false`.
  - `TestVM_Disappears` — `DeleteVMOutOfBand` in a `PreConfig`, then plan step expects `ResourceActionCreate`.
  - `TestVM_ForbiddenIsError` — `WithForbidden()` → `ExpectError: regexp.MustCompile("scope")`.
  - `TestVM_ReplaceOnImageChange` — changing `image` → `plancheck.ResourceActionReplace`.
- [ ] **Step 2: Run** `go test ./internal/provider/ -run TestVM_` → FAIL. **Step 3: Implement.** **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat: add simplevm_vm resource`.

### Task 7: `simplevm_volume` resource

**Files:**
- Create: `internal/provider/volume_resource.go`, `volume_resource_test.go`, `examples/resources/simplevm_volume/{resource.tf,import.sh}`
- Modify: `provider.go`

**Interfaces:**
- Schema: `id` (uuid), `project_id` (opt+computed, RequiresReplace), `name` (required, in-place), `size` (required int64 GiB, ≥1; in-place grow; `int64planmodifier.RequiresReplaceIf` when new < old), `source_volume_id`, `volume_snapshot_id` (optional, RequiresReplace), `timeouts`; computed `openstack_id`, `status`, `device`, `path`.
- Create: `CreateVolume` → wait `available` (failure statuses → error). Read: `GetVolume`, not found → remove; sets name/size(`storage`)/status/device/path; `project_id` only on import. Update: name change → `VolumeAction{OSAction:"rename", NewName}`; size grow → `{OSAction:"extend", Size}` + wait `available`/`in-use` (whichever it was before). Delete: `DeleteVolume` (404 ok) + wait not found. Import passthrough.

- [ ] **Step 1: Write failing tests:** `TestVolume_Lifecycle` (create 10 → rename + extend to 20 in place, assert `ResourceActionUpdate` → import verify), `TestVolume_ShrinkReplaces` (20 → 10 → `ResourceActionReplace`), `TestVolume_Disappears`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `go test ./internal/provider/ -run TestVolume_` → PASS.
- [ ] **Step 5: Commit** `feat: add simplevm_volume resource`.

### Task 8: `simplevm_volume_attachment` resource

**Files:**
- Create: `internal/provider/volume_attachment_resource.go`, `volume_attachment_resource_test.go`, `examples/resources/simplevm_volume_attachment/{resource.tf,import.sh}`
- Modify: `provider.go`

**Interfaces:**
- Schema: `id` (computed `"<volume_id>/<vm_id>"`), `volume_id`, `vm_id` (required, RequiresReplace), `timeouts`; computed `device`, `path`.
- Attached test: `func isAttachedTo(vol *client.Volume, vm *client.VM) bool` — volume `virtual_machine` non-nil and (`openstack_id` equal when both set, else `name` equal). Schema's `VolumeMachine` has no uuid, hence this match.
- Create: `VolumeAction{attach, VMUUID}` → wait `in-use` and `isAttachedTo`. Read: volume or VM not found, or not attached → remove. Delete: `VolumeAction{detach}` → wait `available` (volume not found → ok). Import: parse `<volume_uuid>/<vm_uuid>`, error otherwise.

- [ ] **Step 1: Write failing tests:** `TestVolumeAttachment_Lifecycle` (vm + volume + attachment; check `device` set; import with id `vol/vm`), `TestVolumeAttachment_DetachOnDestroy` (CheckDestroy: fake volume status `available`), `TestVolumeAttachment_BadImportID` (`ExpectError` "expected <volume_id>/<vm_id>"), `TestIsAttachedTo` (unit, table).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat: add simplevm_volume_attachment resource`.

### Task 9: `simplevm_snapshot` resource

**Files:**
- Create: `internal/provider/snapshot_resource.go`, `snapshot_resource_test.go`, `examples/resources/simplevm_snapshot/resource.tf`
- Modify: `provider.go`

**Interfaces:**
- Schema: `id` (computed = openstack_id), `vm_id`, `name`, `description` (required, RequiresReplace), `project_id` (opt+computed, RequiresReplace), `timeouts` (create 10m); computed `status`, `created_at`.
- Create: `CreateSnapshot` → `id` from response `openstack_id`; wait until `ListSnapshotImages` contains it (fallback match exact `name` when response openstack_id empty, then set id from the image) with status `active`.
- Read: found → update status; not found → remove from state (it existed at create).
- Delete: no API call; `resp.Diagnostics.AddWarning("Snapshot not deleted in SimpleVM", "The SimpleVM API cannot delete snapshots. Snapshot %q (%s) was removed from state only; delete it in the SimpleVM portal under Images/Snapshots.")`.
- No import (document).

- [ ] **Step 1: Write failing tests:** `TestSnapshot_Lifecycle` (vm + snapshot, status `active`; fake request log has exactly one `POST snapshots/` after two applies — no duplicate creation on refresh), `TestSnapshot_DestroyWarnsAndKeepsImage` (CheckDestroy: fake still lists the snapshot image).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat: add simplevm_snapshot resource`.

### Task 10: Real-API acceptance tests

**Files:**
- Create: `internal/provider/acc_test.go`

**Interfaces:**
- `func testAccPreCheck(t *testing.T)` — fails test if `SIMPLEVM_API_KEY` or `SIMPLEVM_PROJECT_ID` unset (only reached when `TF_ACC=1`, `resource.Test` skips otherwise).
- Names prefixed `tfacc-<random 6>` (`acctest.RandStringFromCharSet`). Flavor/image chosen via data sources: env `SIMPLEVM_ACC_FLAVOR` (default `de.NBI default`), `SIMPLEVM_ACC_IMAGE` (default first image without `resenv_template_name` whose name contains `Ubuntu`), `SIMPLEVM_ACC_RESENV_IMAGE` (optional; research env test skips when unset).

- [ ] **Step 1: Write** `TestAccDataSources`, `TestAccVM_FullLifecycle` (VM + volume 1 GiB + attachment + stop/resume + snapshot; destroy), `TestAccVM_ResearchEnvironment` (skips without `SIMPLEVM_ACC_RESENV_IMAGE`).
- [ ] **Step 2: Verify gating:** `go test ./internal/provider/ -run TestAcc -v` without `TF_ACC` → all `SKIP`. With real credentials (if available locally) `make testacc` → PASS; record any request-shape corrections back into client + spec "assumed" notes.
- [ ] **Step 3: Commit** `test: add real-API acceptance tests`.

### Task 11: Documentation, examples and example module

**Files:**
- Create: `examples/provider/provider.tf`, `templates/index.md.tmpl`, `examples/modules/research-vm/*`, `wiki/simple_vm/opentofu.md`, `README.md`, `CONTRIBUTING.md`, `CHANGELOG.md`, `development/architecture.md`, `development/testing.md`, `development/releasing.md`
- Generate: `docs/**` via `make docs`

**Content decisions:**
- Every schema attribute has a `MarkdownDescription` (check in Task's verification); resource descriptions mention async behaviour and the snapshot-delete limitation.
- `templates/index.md.tmpl`: authentication (creating a key in the project's API Key tab; scopes needed: `project:read`, `vm:read`, VM/volume change scopes, `volume:change_state`), env vars, OpenTofu vs Terraform `required_providers` (same `source = "deNBI/simplevm"`), limitations (no clusters, no member management, owner = key owner).
- Example module `research-vm`: inputs `name`, `project_id`, `flavor`, `image`, `research_environment_template` (nullable), `volume_size_gb` (nullable), `power_state`; creates VM, optional volume + attachment; outputs `vm_id`, `fixed_ip`, `research_environment_url`, `volume_id`. Includes `versions.tf` with `required_providers`.
- `wiki/simple_vm/opentofu.md` (MkDocs Material style of the SimpleVM wiki: `#` title, admonitions `!!! info`/`!!! warning`, numbered steps): Overview; Prerequisites (API key beta, request access, scopes, expiry); Installing OpenTofu/Terraform; Quickstart (provider block, env var, `tofu init/plan/apply`); Recipes: base-image VM, research environment VM (find template via `simplevm_image`), add a volume, stop/start via `power_state`, snapshot (and that destroy doesn't delete it), using the example module; Importing existing VMs/volumes; Limitations & FAQ (clusters, user management, timeouts, "own objects only" keys); Links to registry docs and GitHub issues.
- `development/*.md`: architecture (package layout, Action↔resource mapping table, polling/state rules, assumed request shapes), testing (unit, fake API, acceptance, running against OpenTofu with `TF_ACC_TERRAFORM_PATH=$(which tofu) TF_ACC_PROVIDER_NAMESPACE=deNBI TF_ACC_PROVIDER_HOST=registry.opentofu.org`, `dev_overrides` in `~/.tofurc`/`~/.terraformrc`), releasing (secrets, tagging `vX.Y.Z`, registry onboarding for both registries); how to add a resource.

- [ ] **Step 1: Write examples + templates; run `make docs`.** Expected: `docs/index.md`, `docs/resources/{vm,volume,volume_attachment,snapshot}.md`, `docs/data-sources/*.md` exist.
- [ ] **Step 2: Run** `go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs validate --provider-name simplevm` → no errors.
- [ ] **Step 3: Validate examples:** with a local `dev_overrides` build, `terraform -chdir=examples/modules/research-vm validate` and same with `tofu` → `Success!`. `terraform fmt -check -recursive examples` → clean.
- [ ] **Step 4: Write wiki page, README, CONTRIBUTING, CHANGELOG, development docs.**
- [ ] **Step 5: Commit** `docs: add reference docs, wiki page, example module and developer docs`.

### Task 12: CI and release pipeline

**Files:**
- Create: `.github/workflows/test.yml`, `.github/workflows/acceptance.yml`, `.github/workflows/release.yml`, `.goreleaser.yml`, `terraform-registry-manifest.json`, `.github/dependabot.yml`

**Content decisions:**
- `test.yml` (push to main, PRs): jobs `build` (setup-go from `go.mod`, `go mod tidy` diff check, `go build`, golangci-lint action v2), `generate` (`make docs` then `git diff --exit-code`), `test` matrix: `{cli: terraform, version: [1.5.*, latest]}` + `{cli: tofu, version: [1.6.*, latest]}` using `hashicorp/setup-terraform` (`terraform_wrapper: false`) / `opentofu/setup-opentofu` (`tofu_wrapper: false`); env `TF_ACC_TERRAFORM_PATH` = path of the installed CLI; for tofu also `TF_ACC_PROVIDER_HOST=registry.opentofu.org`, `TF_ACC_PROVIDER_NAMESPACE=deNBI`; run `go test ./... -count=1 -race -timeout 20m`.
- `acceptance.yml`: `workflow_dispatch` (input `cli`: terraform|tofu) + weekly `schedule`; `concurrency: acceptance` (one real-cloud run at a time); env from secrets; `make testacc`; skip with notice when `SIMPLEVM_API_KEY` secret empty.
- `release.yml`: on tag `v*`; `crazy-max/ghaction-import-gpg` with `GPG_PRIVATE_KEY`/`PASSPHRASE`; `goreleaser/goreleaser-action` v6 `release --clean`; `permissions: contents: write`.
- `.goreleaser.yml`: HashiCorp scaffolding layout (version 2; builds for linux/darwin/windows/freebsd × amd64/arm64/arm/386 minus unsupported; `-s -w -X main.version={{.Version}}`; zip archives `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`; SHA256SUMS + signature; manifest extra file).
- `terraform-registry-manifest.json`: `{"version":1,"metadata":{"protocol_versions":["6.0"]}}`.

- [ ] **Step 1: Write files.** **Step 2: Verify locally:** `goreleaser check` (or `go run github.com/goreleaser/goreleaser/v2@latest check`) → valid; `actionlint` if available (`go run github.com/rhysd/actionlint/cmd/actionlint@latest`) → no errors.
- [ ] **Step 3: Full local verification:** `make lint test` → PASS; with OpenTofu: `TF_ACC_TERRAFORM_PATH=$(which tofu) TF_ACC_PROVIDER_HOST=registry.opentofu.org TF_ACC_PROVIDER_NAMESPACE=deNBI go test ./internal/provider/ -count=1` → PASS.
- [ ] **Step 4: Commit** `ci: add test, acceptance and release workflows`.
