// Package fakeapi is an in-process, stateful fake of the SimpleVM REST API for
// tests. Asynchronous operations advance one step per GET that observes the
// object, so tests are deterministic and need no timers.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
)

const (
	// APIKey is the only API key the fake accepts.
	APIKey = "test-key"
	// ProjectID is the id of the single seeded project.
	ProjectID int64 = 1

	pathPrefix      = "/portal/api/"
	defaultPageSize = 10
	createdAt       = "2026-01-01T00:00:00Z"
)

// RecordedRequest is one request received by the fake, including rejected ones.
// Path is the full URL path (e.g. "/portal/api/vms/") without the query.
type RecordedRequest struct {
	Method string
	Path   string
	Body   []byte
}

// Option configures a Server.
type Option func(*Server)

// WithPlaybookFailure makes research environments end with task_state
// "playbook_failed" instead of completing.
func WithPlaybookFailure() Option { return func(s *Server) { s.playbookFailure = true } }

// WithIdleTaskState makes VMs report taskState instead of an empty
// task_state once they are active or stopped and idle, as SimpleVM does with
// for example "playbook_successful" or "checking_status".
func WithIdleTaskState(taskState string) Option {
	return func(s *Server) { s.idleTaskState = taskState }
}

// WithPlaybookDoneNeverReported makes research environments never report
// playbook[].done = true, even after the playbook succeeded.
func WithPlaybookDoneNeverReported() Option {
	return func(s *Server) { s.playbookDoneNeverReported = true }
}

// WithSnapshotsStuckQueued makes snapshots stay "queued" forever.
func WithSnapshotsStuckQueued() Option { return func(s *Server) { s.snapshotsStuck = true } }

// WithSnapshotsUnlisted makes the snapshot images listing omit every
// snapshot, as when SimpleVM lists a new snapshot late.
func WithSnapshotsUnlisted() Option { return func(s *Server) { s.snapshotsUnlisted = true } }

// WithForbidden makes every request answer 403.
func WithForbidden() Option { return func(s *Server) { s.forbidden = true } }

type vmStep struct {
	vmState      string
	taskState    string
	playbookDone bool
	gone         bool
}

type vmRec struct {
	uuid, openstackID, name, imageName string
	flavor                             client.Flavor
	vmState, taskState                 string
	resenv                             bool
	playbookID                         int64
	playbookDone                       bool
	fixedIP                            string
	pending                            []vmStep
}

type volStep struct {
	status  string
	storage int64 // 0 keeps the current size
	gone    bool
}

type volRec struct {
	uuid, openstackID, name, status string
	storage                         int64
	vmUUID, device                  string
	pending                         []volStep
}

// injection answers matching requests with a canned response instead of
// handling them.
type injection struct {
	method, path string // path relative to the API root; "*" matches one segment
	remaining    int
	status       int
	body         any
}

func (in *injection) matches(method, rel string) bool {
	if in.remaining <= 0 || in.method != method {
		return false
	}
	want, got := strings.Split(in.path, "/"), strings.Split(rel, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != "*" && want[i] != got[i] {
			return false
		}
	}
	return true
}

type snapRec struct {
	name, description, openstackID, status string
	activate                               bool // next observation turns it active
}

// Server is the fake API.
type Server struct {
	t   testing.TB
	srv *httptest.Server

	mu              sync.Mutex
	playbookFailure bool
	forbidden       bool
	// idleTaskState replaces an empty task_state of active or stopped VMs.
	idleTaskState             string
	playbookDoneNeverReported bool
	snapshotsStuck            bool
	snapshotsUnlisted         bool
	seq                       int64
	vms                       map[string]*vmRec
	vmOrder                   []string
	vols                      map[string]*volRec
	volOrder                  []string
	snaps                     []*snapRec
	requests                  []RecordedRequest
	injections                []*injection
	flavors                   []client.Flavor
	images                    []client.Image
}

// New starts a fake server that is closed when the test ends.
func New(t testing.TB, opts ...Option) *Server {
	t.Helper()
	s := &Server{
		t:    t,
		vms:  map[string]*vmRec{},
		vols: map[string]*volRec{},
		flavors: []client.Flavor{
			{ID: 1, Name: "de.NBI default", VCPUs: 4, RAM: 8192, RootDisk: 20, CreditsCostPerHour: 1},
			{ID: 2, Name: "de.NBI small", VCPUs: 2, RAM: 4096, RootDisk: 20, CreditsCostPerHour: 0.5},
		},
		images: []client.Image{
			{OpenstackID: "img-ubuntu-2204", Name: "Ubuntu-22.04", Title: "Ubuntu-22.04", Status: "active",
				OSDistro: "ubuntu", OSVersion: "22.04", Tags: []string{"base"}, MinRAM: 1024, MinDisk: 10, CreatedAt: createdAt},
			{OpenstackID: "img-rstudio-2204", Name: "RStudio-Ubuntu-22.04", Title: "RStudio", Status: "active",
				OSDistro: "ubuntu", OSVersion: "22.04", Tags: []string{"rstudio"}, MinRAM: 4096, MinDisk: 20,
				ResenvTemplateName: "rstudio", CreatedAt: createdAt},
		},
	}
	for _, o := range opts {
		o(s)
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the API base URL, ending in "/portal/api/".
func (s *Server) URL() string { return s.srv.URL + pathPrefix }

// Requests returns a copy of every request received so far, in order.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// DeleteVMOutOfBand removes a VM as if it was deleted in the UI. Its volumes
// become available.
func (s *Server) DeleteVMOutOfBand(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vms[uuid]; !ok {
		s.t.Errorf("fakeapi: DeleteVMOutOfBand: unknown VM %q", uuid)
		return
	}
	s.removeVM(uuid)
}

// DeleteVolumeOutOfBand removes a volume as if it was deleted in the UI.
func (s *Server) DeleteVolumeOutOfBand(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vols[uuid]; !ok {
		s.t.Errorf("fakeapi: DeleteVolumeOutOfBand: unknown volume %q", uuid)
		return
	}
	s.removeVolume(uuid)
}

// DeleteSnapshotOutOfBand removes the snapshot image with the given name as
// if it was deleted in the UI.
func (s *Server) DeleteSnapshotOutOfBand(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sn := range s.snaps {
		if sn.name == name {
			s.snaps = append(s.snaps[:i], s.snaps[i+1:]...)
			return
		}
	}
	s.t.Errorf("fakeapi: DeleteSnapshotOutOfBand: unknown snapshot %q", name)
}

// SetVMState forces a VM into the given states and cancels its pending steps.
func (s *Server) SetVMState(uuid, vmState, taskState string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vm, ok := s.vms[uuid]
	if !ok {
		s.t.Errorf("fakeapi: SetVMState: unknown VM %q", uuid)
		return
	}
	vm.vmState, vm.taskState, vm.pending = vmState, taskState, nil
}

// FailRequests makes the next n requests with the given method and path
// answer status with an error body, without touching any state. path is
// relative to the API root and ends in "/", e.g. "vms/" or "vms/*/action/";
// "*" matches one path segment. Injections are matched before routing, in the
// order they were added.
func (s *Server) FailRequests(method, path string, n, status int) {
	s.RespondTo(method, path, n, status, map[string]string{"detail": http.StatusText(status)})
}

// RespondTo makes the next n requests with the given method and path (as for
// FailRequests) answer status with body, without touching any state.
func (s *Server) RespondTo(method, path string, n, status int, body any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.injections = append(s.injections, &injection{method: method, path: path, remaining: n, status: status, body: body})
}

// ---- internals (callers hold s.mu) ----

func (s *Server) nextID(prefix string) (uuid, osID string) {
	s.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.seq), fmt.Sprintf("%s-%012d", prefix, s.seq)
}

func (s *Server) removeVM(uuid string) {
	delete(s.vms, uuid)
	for i, id := range s.vmOrder {
		if id == uuid {
			s.vmOrder = append(s.vmOrder[:i], s.vmOrder[i+1:]...)
			break
		}
	}
	for _, v := range s.vols {
		if v.vmUUID == uuid {
			v.status, v.vmUUID, v.device, v.pending = client.VolumeStatusAvailable, "", "", nil
		}
	}
}

func (s *Server) removeVolume(uuid string) {
	delete(s.vols, uuid)
	for i, id := range s.volOrder {
		if id == uuid {
			s.volOrder = append(s.volOrder[:i], s.volOrder[i+1:]...)
			break
		}
	}
}

// advanceVM performs one step and reports whether the VM still exists.
func (s *Server) advanceVM(vm *vmRec) bool {
	if len(vm.pending) == 0 {
		return true
	}
	st := vm.pending[0]
	vm.pending = vm.pending[1:]
	if st.gone {
		s.removeVM(vm.uuid)
		return false
	}
	vm.vmState, vm.taskState = st.vmState, st.taskState
	if vm.taskState == "" && (vm.vmState == client.VMStateActive || vm.vmState == client.VMStateStopped) {
		vm.taskState = s.idleTaskState
	}
	if vm.resenv && !s.playbookDoneNeverReported {
		vm.playbookDone = st.playbookDone
	}
	return true
}

func (s *Server) advanceVolume(v *volRec) bool {
	if len(v.pending) == 0 {
		return true
	}
	st := v.pending[0]
	v.pending = v.pending[1:]
	if st.gone {
		s.removeVolume(v.uuid)
		return false
	}
	v.status = st.status
	if st.storage != 0 {
		v.storage = st.storage
	}
	if st.status == client.VolumeStatusAvailable {
		v.vmUUID, v.device = "", ""
	}
	return true
}

func (s *Server) vmJSON(vm *vmRec) client.VM {
	osID := vm.openstackID
	pid := ProjectID
	created := createdAt
	out := client.VM{
		UUID: vm.uuid, Name: vm.name, OpenstackID: &osID, VMState: vm.vmState,
		FixedIP: vm.fixedIP, ImageName: vm.imageName, Project: &pid, Flavor: vm.flavor, CreatedAtDate: &created,
	}
	// Research environment VMs report "" once idle, others null: the provider
	// has to cope with both.
	if vm.taskState != "" || vm.resenv {
		ts := vm.taskState
		out.TaskState = &ts
	}
	if vm.resenv {
		out.Playbook = client.Playbooks{{ID: vm.playbookID, Done: vm.playbookDone}}
		if vm.playbookDone {
			out.ResearchEnvironmentURL = "https://simplevm.example/" + vm.uuid + "/"
		}
	}
	return out
}

func (s *Server) volumeJSON(v *volRec) client.Volume {
	osID := v.openstackID
	out := client.Volume{
		UUID: v.uuid, Name: v.name, Status: v.status, Storage: v.storage, OpenstackID: &osID,
		ProjectID: json.Number(strconv.FormatInt(ProjectID, 10)),
	}
	if v.vmUUID != "" && (v.status == client.VolumeStatusInUse || v.status == "detaching") {
		out.Device = v.device
		out.Path = "/media/volume/" + v.name
		if vm, ok := s.vms[v.vmUUID]; ok {
			vmOS := vm.openstackID
			out.VirtualMachine = &client.VolumeMachine{Name: vm.name, OpenstackID: &vmOS, VMState: vm.vmState}
		}
	}
	return out
}

func (s *Server) snapshotImages() []client.Image {
	out := []client.Image{}
	if s.snapshotsUnlisted {
		return out
	}
	for _, sn := range s.snaps {
		if sn.activate {
			sn.status, sn.activate = "active", false
		}
		out = append(out, client.Image{
			OpenstackID: sn.openstackID, Name: sn.name, Title: sn.name, Description: sn.description,
			Status: sn.status, Tags: []string{}, IsSnapshot: true, CreatedAt: createdAt,
		})
	}
	return out
}

// ---- HTTP ----

type response struct {
	status int
	body   any
}

func detail(status int, msg string) response {
	return response{status, map[string]string{"detail": msg}}
}

var notFound = detail(http.StatusNotFound, "Not found.")

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, RecordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})

	var resp response
	switch {
	case s.forbidden:
		resp = detail(http.StatusForbidden, "You do not have permission to perform this action.")
	case r.Header.Get("X-API-KEY") != APIKey:
		resp = detail(http.StatusUnauthorized, "Invalid API key.")
	case !strings.HasSuffix(r.URL.Path, "/"):
		// Django's APPEND_SLASH: bugs where a client drops the slash show up as 301.
		w.Header().Set("Location", r.URL.Path+"/")
		w.WriteHeader(http.StatusMovedPermanently)
		return
	default:
		resp = s.injected(r)
		if resp.status == 0 {
			resp = s.route(r, body)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	if resp.body != nil {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(resp.body)
	}
}

// injected returns the canned response of the first matching injection, or a
// zero response.
func (s *Server) injected(r *http.Request) response {
	rel := strings.TrimPrefix(r.URL.Path, pathPrefix)
	for _, in := range s.injections {
		if in.matches(r.Method, rel) {
			in.remaining--
			return response{in.status, in.body}
		}
	}
	return response{}
}

func (s *Server) route(r *http.Request, body []byte) response {
	rel, ok := strings.CutPrefix(r.URL.Path, pathPrefix)
	if !ok {
		return notFound
	}
	p := strings.Split(strings.TrimSuffix(rel, "/"), "/")
	m := r.Method
	bad := detail(http.StatusMethodNotAllowed, "Method not allowed.")
	switch {
	case p[0] == "projects" && len(p) == 1:
		return only(m, http.MethodGet, bad, func() response { return page([]client.Project{s.project()}) })
	case p[0] == "projects" && len(p) == 2 && p[1] == "by-api-key":
		return only(m, http.MethodGet, bad, func() response { return response{http.StatusOK, s.project()} })
	case p[0] == "projects" && len(p) >= 3 && p[1] == strconv.FormatInt(ProjectID, 10):
		return only(m, http.MethodGet, bad, func() response { return s.projectSub(p[2:]) })
	case p[0] == "vms" && len(p) == 1 && m == http.MethodGet:
		return s.listVMs(r)
	case p[0] == "vms" && len(p) == 1 && m == http.MethodPost:
		return s.createVM(body)
	case p[0] == "vms" && len(p) == 2:
		return only(m, http.MethodDelete, bad, func() response { return s.deleteVM(p[1]) })
	case p[0] == "vms" && len(p) == 3 && p[2] == "action":
		return only(m, http.MethodPost, bad, func() response { return s.vmAction(p[1], body) })
	case p[0] == "volumes" && len(p) == 1:
		return only(m, http.MethodPost, bad, func() response { return s.createVolume(body) })
	case p[0] == "volumes" && len(p) == 2 && p[1] == "by-api-key":
		return only(m, http.MethodGet, bad, s.listVolumes)
	case p[0] == "volumes" && len(p) == 2 && m == http.MethodGet:
		return s.getVolume(p[1])
	case p[0] == "volumes" && len(p) == 2:
		return only(m, http.MethodDelete, bad, func() response { return s.deleteVolume(p[1]) })
	case p[0] == "volumes" && len(p) == 3 && p[2] == "action":
		return only(m, http.MethodPost, bad, func() response { return s.volumeAction(p[1], body) })
	case p[0] == "snapshots" && len(p) == 1:
		return only(m, http.MethodPost, bad, func() response { return s.createSnapshot(body) })
	}
	return notFound
}

func only(method, want string, bad response, f func() response) response {
	if method != want {
		return bad
	}
	return f()
}

func (s *Server) project() client.Project {
	return client.Project{
		ID: ProjectID, Shortname: "TestProject", Description: "Fake project", VMsLimit: 10, CoresLimit: 40,
		RAMLimit: 163840, VolumeCounterLimit: 10, VolumeStorageLimit: 100, IsMember: true,
	}
}

// page wraps items in a single DRF page.
func page[T any](items []T) response {
	if items == nil {
		items = []T{}
	}
	return response{http.StatusOK, map[string]any{"count": len(items), "next": nil, "previous": nil, "results": items}}
}

func (s *Server) projectSub(p []string) response {
	switch {
	case len(p) == 1 && p[0] == "flavors":
		return response{http.StatusOK, s.flavors}
	case len(p) == 1 && p[0] == "images":
		return response{http.StatusOK, s.images}
	case len(p) == 2 && p[0] == "snapshots" && p[1] == "images":
		return response{http.StatusOK, s.snapshotImages()}
	}
	return notFound
}

func decode(body []byte, v any) *response {
	if err := json.Unmarshal(body, v); err != nil {
		r := detail(http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return &r
	}
	return nil
}

func (s *Server) imageKnown(name string) bool {
	for _, im := range s.images {
		if im.Name == name {
			return true
		}
	}
	for _, sn := range s.snaps {
		if sn.name == name {
			return true
		}
	}
	return false
}

// ---- VMs ----

func (s *Server) listVMs(r *http.Request) response {
	q := r.URL.Query()
	pageNo, size := 1, defaultPageSize
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return detail(http.StatusNotFound, "Invalid page.")
		}
		pageNo = n
	}
	if v := q.Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}
	total := len(s.vmOrder)
	start := (pageNo - 1) * size
	if start >= total && (pageNo != 1 || total != 0) {
		return detail(http.StatusNotFound, "Invalid page.")
	}
	end := min(start+size, total)
	window := append([]string(nil), s.vmOrder[start:end]...)

	results := []client.VM{}
	for _, id := range window {
		vm := s.vms[id]
		if s.advanceVM(vm) {
			results = append(results, s.vmJSON(vm))
		}
	}
	link := func(n int) any {
		u := url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path}
		u.RawQuery = fmt.Sprintf("page=%d&page_size=%d", n, size)
		return u.String()
	}
	var next, prev any
	if end < total {
		next = link(pageNo + 1)
	}
	if pageNo > 1 {
		prev = link(pageNo - 1)
	}
	return response{http.StatusOK, map[string]any{"count": total, "next": next, "previous": prev, "results": results}}
}

func (s *Server) createVM(body []byte) response {
	var req client.CreateVMRequest
	if bad := decode(body, &req); bad != nil {
		return *bad
	}
	if req.ProjectID != ProjectID {
		return detail(http.StatusForbidden, "No access to this project.")
	}
	if req.VMName == "" || !s.imageKnown(req.ImageName) {
		return detail(http.StatusBadRequest, "Invalid vm_name or image_name.")
	}
	var flavor *client.Flavor
	for i := range s.flavors {
		if s.flavors[i].Name == req.FlavorName {
			flavor = &s.flavors[i]
		}
	}
	if flavor == nil {
		return detail(http.StatusBadRequest, "Unknown flavor_name.")
	}
	uuid, osID := s.nextID("os-vm")
	vm := &vmRec{
		uuid: uuid, openstackID: osID, name: req.VMName, imageName: req.ImageName, flavor: *flavor,
		vmState: "planned", resenv: req.ResearchEnvironment != nil, playbookID: s.seq,
		fixedIP: fmt.Sprintf("10.0.0.%d", s.seq+1),
	}
	vm.pending = []vmStep{{vmState: client.VMStateBuilding}, {vmState: client.VMStateActive}}
	if vm.resenv {
		last := vmStep{vmState: client.VMStateActive, playbookDone: true}
		if s.playbookFailure {
			last = vmStep{vmState: client.VMStateActive, taskState: client.TaskStatePlaybookFailed}
		}
		vm.pending = []vmStep{
			{vmState: client.VMStateBuilding},
			{vmState: client.VMStateActive, taskState: "prepare_playbook_build"},
			{vmState: client.VMStateActive, taskState: "building_playbook"},
			last,
		}
	}
	s.vms[uuid] = vm
	s.vmOrder = append(s.vmOrder, uuid)
	return response{http.StatusCreated, client.VMActionResponse{
		UUID: uuid, OpenstackID: osID, OSAction: "create", VMState: vm.vmState,
	}}
}

func (s *Server) deleteVM(uuid string) response {
	vm, ok := s.vms[uuid]
	if !ok || vm.vmState == client.VMStateDeleted {
		return notFound
	}
	for _, v := range s.vols {
		if v.vmUUID == uuid {
			v.status, v.vmUUID, v.device, v.pending = client.VolumeStatusAvailable, "", "", nil
		}
	}
	vm.pending = []vmStep{{vmState: client.VMStateDeleted}, {gone: true}}
	return response{status: http.StatusNoContent}
}

func (s *Server) vmAction(uuid string, body []byte) response {
	vm, ok := s.vms[uuid]
	if !ok || vm.vmState == client.VMStateDeleted {
		return notFound
	}
	var req struct {
		OSAction string `json:"os_action"`
	}
	if bad := decode(body, &req); bad != nil {
		return *bad
	}
	switch {
	case req.OSAction == client.VMActionStop && vm.vmState == client.VMStateActive && len(vm.pending) == 0:
		vm.taskState = "powering-off"
		vm.pending = []vmStep{{vmState: client.VMStateStopped}}
	case req.OSAction == client.VMActionResume && vm.vmState == client.VMStateStopped && len(vm.pending) == 0:
		vm.taskState = "powering-on"
		vm.pending = []vmStep{{vmState: client.VMStateActive}}
	case req.OSAction == client.VMActionStop || req.OSAction == client.VMActionResume:
		return detail(http.StatusConflict, fmt.Sprintf("Cannot %s a VM in state %q.", req.OSAction, vm.vmState))
	default:
		return detail(http.StatusBadRequest, "Unsupported os_action.")
	}
	return response{http.StatusOK, client.VMActionResponse{
		UUID: uuid, OpenstackID: vm.openstackID, OSAction: req.OSAction, VMState: vm.vmState, TaskState: vm.taskState,
	}}
}

// ---- volumes ----

// listVolumes is read-only: only GET volumes/{uuid}/ advances a volume.
func (s *Server) listVolumes() response {
	out := []client.Volume{}
	for _, id := range s.volOrder {
		out = append(out, s.volumeJSON(s.vols[id]))
	}
	return page(out)
}

func (s *Server) getVolume(uuid string) response {
	v, ok := s.vols[uuid]
	if !ok {
		return notFound
	}
	if !s.advanceVolume(v) {
		return notFound
	}
	return response{http.StatusOK, s.volumeJSON(v)}
}

func (s *Server) createVolume(body []byte) response {
	var req client.CreateVolumeRequest
	if bad := decode(body, &req); bad != nil {
		return *bad
	}
	if req.ProjectID != ProjectID {
		return detail(http.StatusForbidden, "No access to this project.")
	}
	if req.Name == "" || req.Storage < 1 {
		return detail(http.StatusBadRequest, "Invalid name or storage.")
	}
	uuid, osID := s.nextID("os-vol")
	s.vols[uuid] = &volRec{
		uuid: uuid, openstackID: osID, name: req.Name, status: "creating", storage: req.Storage,
		pending: []volStep{{status: client.VolumeStatusAvailable}},
	}
	s.volOrder = append(s.volOrder, uuid)
	return response{http.StatusCreated, client.VolumeActionResponse{
		UUID: uuid, OpenstackID: osID, OSAction: "create", VolumeStatus: "creating", Name: req.Name, Size: req.Storage,
	}}
}

func (s *Server) deleteVolume(uuid string) response {
	v, ok := s.vols[uuid]
	if !ok || v.status == client.VolumeStatusDeleted {
		return notFound
	}
	v.pending = []volStep{{status: client.VolumeStatusDeleted}, {gone: true}}
	return response{status: http.StatusNoContent}
}

func (s *Server) volumeAction(uuid string, body []byte) response {
	v, ok := s.vols[uuid]
	if !ok || v.status == client.VolumeStatusDeleted {
		return notFound
	}
	var req client.VolumeActionRequest
	if bad := decode(body, &req); bad != nil {
		return *bad
	}
	conflict := func() response {
		return detail(http.StatusConflict, fmt.Sprintf("Cannot %s a volume with status %q.", req.OSAction, v.status))
	}
	switch req.OSAction {
	case "attach":
		vm, ok := s.vms[req.VMUUID]
		if !ok || vm.vmState == client.VMStateDeleted {
			return detail(http.StatusNotFound, "VM not found.")
		}
		if v.status != client.VolumeStatusAvailable {
			return conflict()
		}
		n := 0
		for _, o := range s.vols {
			if o.vmUUID == req.VMUUID {
				n++
			}
		}
		v.status, v.vmUUID, v.device = "attaching", req.VMUUID, "/dev/vd"+string(rune('b'+n))
		v.pending = []volStep{{status: client.VolumeStatusInUse}}
	case "detach":
		if v.status != client.VolumeStatusInUse {
			return conflict()
		}
		v.status = "detaching"
		v.pending = []volStep{{status: client.VolumeStatusAvailable}}
	case "extend":
		if v.status != client.VolumeStatusAvailable {
			return conflict()
		}
		if req.Size <= v.storage {
			return detail(http.StatusBadRequest, "size must be larger than the current size.")
		}
		v.status = "extending"
		v.pending = []volStep{{status: client.VolumeStatusAvailable, storage: req.Size}}
	case "rename":
		if req.NewName == "" {
			return detail(http.StatusBadRequest, "new_name must not be empty.")
		}
		v.name = req.NewName
	default:
		return detail(http.StatusBadRequest, "Unsupported os_action.")
	}
	return response{http.StatusOK, s.volumeJSON(v)}
}

// ---- snapshots ----

func (s *Server) createSnapshot(body []byte) response {
	var req client.CreateSnapshotRequest
	if bad := decode(body, &req); bad != nil {
		return *bad
	}
	vm, ok := s.vms[req.VMUUID]
	if !ok || vm.vmState == client.VMStateDeleted {
		return detail(http.StatusNotFound, "VM not found.")
	}
	if req.Name == "" {
		return detail(http.StatusBadRequest, "name must not be empty.")
	}
	_, osID := s.nextID("os-snap")
	s.snaps = append(s.snaps, &snapRec{name: req.Name, description: req.Description, openstackID: osID, status: "queued", activate: !s.snapshotsStuck})
	return response{http.StatusCreated, client.Snapshot{
		Name: req.Name, OpenstackID: osID, Status: "queued", Description: req.Description, CreatedAt: createdAt, ProjectID: ProjectID,
	}}
}
