package fakeapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/deNBI/terraform-provider-simplevm/internal/fakeapi"
)

func newClient(t *testing.T, srv *fakeapi.Server) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Endpoint: srv.URL(), APIKey: fakeapi.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitVM(t *testing.T, c *client.Client, uuid string, want func(*client.VM) bool) *client.VM {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got *client.VM
	err := client.WaitFor(ctx, time.Millisecond, func(ctx context.Context) (bool, string, error) {
		vm, err := c.GetVM(ctx, uuid)
		if err != nil {
			return false, "", err
		}
		got = vm
		return want(vm), vm.VMState, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func waitVolume(t *testing.T, c *client.Client, uuid, status string) *client.Volume {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got *client.Volume
	err := client.WaitFor(ctx, time.Millisecond, func(ctx context.Context) (bool, string, error) {
		v, err := c.GetVolume(ctx, uuid)
		if err != nil {
			return false, "", err
		}
		got = v
		return v.Status == status, v.Status, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func createVM(t *testing.T, c *client.Client, name, image string, resenv *client.ResearchEnvironmentBackend) string {
	t.Helper()
	resp, err := c.CreateVM(context.Background(), client.CreateVMRequest{
		ProjectID: fakeapi.ProjectID, ImageName: image, FlavorName: "de.NBI small", VMName: name, ResearchEnvironment: resenv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OpenstackID == "" || resp.UUID == "" {
		t.Fatalf("create response lacks ids: %+v", resp)
	}
	return resp.UUID
}

func isActive(vm *client.VM) bool { return vm.VMState == client.VMStateActive }

func TestFake_VMLifecycle(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()

	uuid := createVM(t, c, "vm1", "Ubuntu-22.04", nil)
	vm := waitVM(t, c, uuid, isActive)
	if vm.OpenstackID == nil || *vm.OpenstackID == "" || vm.Name != "vm1" || vm.Flavor.Name != "de.NBI small" {
		t.Fatalf("unexpected VM: %+v", vm)
	}

	if _, err := c.VMAction(ctx, uuid, client.VMActionStop); err != nil {
		t.Fatal(err)
	}
	waitVM(t, c, uuid, func(vm *client.VM) bool { return vm.VMState == client.VMStateStopped })

	if _, err := c.VMAction(ctx, uuid, client.VMActionResume); err != nil {
		t.Fatal(err)
	}
	waitVM(t, c, uuid, isActive)

	if err := c.DeleteVM(ctx, uuid); err != nil {
		t.Fatal(err)
	}
	ctxT, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := client.WaitFor(ctxT, time.Millisecond, func(ctx context.Context) (bool, string, error) {
		_, err := c.GetVM(ctx, uuid)
		if client.IsNotFound(err) {
			return true, "", nil
		}
		return false, "still there", err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteVM(ctx, uuid); !client.IsNotFound(err) {
		t.Fatalf("second delete: want not found, got %v", err)
	}
}

func TestFake_VMStepsAreOnePerObservation(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	uuid := createVM(t, c, "r", "RStudio-Ubuntu-22.04", &client.ResearchEnvironmentBackend{Template: "rstudio"})

	var seen []string
	for range 6 {
		vm, err := c.GetVM(ctx, uuid)
		if err != nil {
			t.Fatal(err)
		}
		task := ""
		if vm.TaskState != nil {
			task = *vm.TaskState
		}
		seen = append(seen, vm.VMState+"/"+task+"/"+boolStr(vm.Playbook.AllDone()))
	}
	want := []string{
		"building//false",
		"active/prepare_playbook_build/false",
		"active/building_playbook/false",
		"active//true",
		"active//true",
		"active//true",
	}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("got  %v\nwant %v", seen, want)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestFake_PlaybookFailure(t *testing.T) {
	srv := fakeapi.New(t, fakeapi.WithPlaybookFailure())
	c := newClient(t, srv)
	uuid := createVM(t, c, "r", "RStudio-Ubuntu-22.04", &client.ResearchEnvironmentBackend{Template: "rstudio"})
	vm := waitVM(t, c, uuid, func(vm *client.VM) bool {
		return vm.TaskState != nil && *vm.TaskState == client.TaskStatePlaybookFailed
	})
	if vm.Playbook.AllDone() {
		t.Fatal("failed playbook must not be done")
	}
}

func TestFake_VolumeAttach(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	vmUUID := createVM(t, c, "vm1", "Ubuntu-22.04", nil)
	waitVM(t, c, vmUUID, isActive)

	created, err := c.CreateVolume(ctx, client.CreateVolumeRequest{ProjectID: fakeapi.ProjectID, Storage: 5, Name: "vol"})
	if err != nil {
		t.Fatal(err)
	}
	if created.VolumeStatus != "creating" {
		t.Fatalf("status %q", created.VolumeStatus)
	}
	waitVolume(t, c, created.UUID, client.VolumeStatusAvailable)

	if _, err := c.VolumeAction(ctx, created.UUID, client.VolumeActionRequest{OSAction: "attach", VMUUID: vmUUID}); err != nil {
		t.Fatal(err)
	}
	v := waitVolume(t, c, created.UUID, client.VolumeStatusInUse)
	if v.VirtualMachine == nil || v.VirtualMachine.Name != "vm1" || v.VirtualMachine.OpenstackID == nil || v.VirtualMachine.VMState != "active" {
		t.Fatalf("bad virtual_machine: %+v", v.VirtualMachine)
	}

	if _, err := c.VolumeAction(ctx, created.UUID, client.VolumeActionRequest{OSAction: "detach", VMUUID: vmUUID}); err != nil {
		t.Fatal(err)
	}
	v = waitVolume(t, c, created.UUID, client.VolumeStatusAvailable)
	if v.VirtualMachine != nil {
		t.Fatalf("still attached: %+v", v.VirtualMachine)
	}

	if _, err := c.VolumeAction(ctx, created.UUID, client.VolumeActionRequest{OSAction: "extend", Size: 10}); err != nil {
		t.Fatal(err)
	}
	if v = waitVolume(t, c, created.UUID, client.VolumeStatusAvailable); v.Storage != 10 {
		t.Fatalf("storage %d", v.Storage)
	}
	if _, err := c.VolumeAction(ctx, created.UUID, client.VolumeActionRequest{OSAction: "rename", NewName: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if v = waitVolume(t, c, created.UUID, client.VolumeStatusAvailable); v.Name != "renamed" {
		t.Fatalf("name %q", v.Name)
	}

	list, err := c.ListVolumes(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}

	if err := c.DeleteVolume(ctx, created.UUID); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err = c.GetVolume(ctx, created.UUID); err != nil {
			break
		}
	}
	if !client.IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestFake_DeleteVMDetachesVolumes(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	vmUUID := createVM(t, c, "vm1", "Ubuntu-22.04", nil)
	waitVM(t, c, vmUUID, isActive)
	vol, _ := c.CreateVolume(ctx, client.CreateVolumeRequest{ProjectID: fakeapi.ProjectID, Storage: 1, Name: "v"})
	waitVolume(t, c, vol.UUID, client.VolumeStatusAvailable)
	if _, err := c.VolumeAction(ctx, vol.UUID, client.VolumeActionRequest{OSAction: "attach", VMUUID: vmUUID}); err != nil {
		t.Fatal(err)
	}
	waitVolume(t, c, vol.UUID, client.VolumeStatusInUse)

	if err := c.DeleteVM(ctx, vmUUID); err != nil {
		t.Fatal(err)
	}
	v := waitVolume(t, c, vol.UUID, client.VolumeStatusAvailable)
	if v.VirtualMachine != nil {
		t.Fatalf("still attached: %+v", v.VirtualMachine)
	}
}

func TestFake_Snapshot(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	vmUUID := createVM(t, c, "vm1", "Ubuntu-22.04", nil)
	waitVM(t, c, vmUUID, isActive)

	snap, err := c.CreateSnapshot(ctx, client.CreateSnapshotRequest{Name: "snap", Description: "d", VMUUID: vmUUID})
	if err != nil {
		t.Fatal(err)
	}
	if snap.OpenstackID == "" || snap.Status != "queued" {
		t.Fatalf("snapshot: %+v", snap)
	}
	var statuses []string
	for range 3 {
		imgs, err := c.ListSnapshotImages(ctx, fakeapi.ProjectID)
		if err != nil || len(imgs) != 1 {
			t.Fatalf("images: %v %v", imgs, err)
		}
		if imgs[0].OpenstackID != snap.OpenstackID || !imgs[0].IsSnapshot || imgs[0].Name != "snap" {
			t.Fatalf("image mismatch: %+v vs %+v", imgs[0], snap)
		}
		statuses = append(statuses, imgs[0].Status)
	}
	if strings.Join(statuses, ",") != "active,active,active" {
		t.Fatalf("statuses %v", statuses)
	}
	// A snapshot image can be used to create a VM.
	createVM(t, c, "from-snap", "snap", nil)
}

func TestFake_SeedData(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	p, err := c.GetProjectByAPIKey(ctx)
	if err != nil || p.ID != fakeapi.ProjectID {
		t.Fatalf("%+v %v", p, err)
	}
	if ps, err := c.ListProjects(ctx); err != nil || len(ps) != 1 {
		t.Fatalf("%v %v", ps, err)
	}
	fl, err := c.ListFlavors(ctx, fakeapi.ProjectID)
	if err != nil || len(fl) != 2 || fl[0].Name != "de.NBI default" || fl[1].Name != "de.NBI small" {
		t.Fatalf("%v %v", fl, err)
	}
	imgs, err := c.ListImages(ctx, fakeapi.ProjectID)
	if err != nil || len(imgs) != 2 || imgs[0].ResenvTemplateName != "" || imgs[1].ResenvTemplateName != "rstudio" {
		t.Fatalf("%v %v", imgs, err)
	}
}

func TestFake_VMListPagination(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	for i := range 3 {
		createVM(t, c, "vm"+string(rune('a'+i)), "Ubuntu-22.04", nil)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL()+"vms/?page=1&page_size=2", nil)
	req.Header.Set("X-API-KEY", fakeapi.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"count":3`) || !strings.Contains(string(b), `"next":"`+srv.URL()+`vms/?page=2&page_size=2"`) {
		t.Fatalf("unexpected page: %s", b)
	}
	// The client follows the next link and sees everything.
	vms, err := c.ListVMs(context.Background())
	if err != nil || len(vms) != 3 {
		t.Fatalf("%v %v", vms, err)
	}
}

func TestFake_OutOfBandAndHooks(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	uuid := createVM(t, c, "vm1", "Ubuntu-22.04", nil)
	waitVM(t, c, uuid, isActive)
	vol, _ := c.CreateVolume(ctx, client.CreateVolumeRequest{ProjectID: fakeapi.ProjectID, Storage: 1, Name: "v"})
	waitVolume(t, c, vol.UUID, client.VolumeStatusAvailable)

	srv.SetVMState(uuid, "error", "")
	if vm, _ := c.GetVM(ctx, uuid); vm == nil || vm.VMState != "error" {
		t.Fatalf("SetVMState not applied: %+v", vm)
	}
	srv.DeleteVMOutOfBand(uuid)
	if _, err := c.GetVM(ctx, uuid); !client.IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
	srv.DeleteVolumeOutOfBand(vol.UUID)
	if _, err := c.GetVolume(ctx, vol.UUID); !client.IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := c.DeleteVM(ctx, "nope"); !client.IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestFake_Auth(t *testing.T) {
	srv := fakeapi.New(t)
	bad, err := client.New(client.Config{Endpoint: srv.URL(), APIKey: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bad.ListVMs(context.Background())
	var ae *client.APIError
	if !asAPIError(err, &ae) || ae.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) == 0 || reqs[0].Method != http.MethodGet || !strings.HasPrefix(reqs[0].Path, "/portal/api/vms/") {
		t.Fatalf("rejected request not recorded: %+v", reqs)
	}
}

func asAPIError(err error, target **client.APIError) bool { return errors.As(err, target) }

func TestFake_Forbidden(t *testing.T) {
	srv := fakeapi.New(t, fakeapi.WithForbidden())
	c := newClient(t, srv)
	_, err := c.ListVMs(context.Background())
	var ae *client.APIError
	if !asAPIError(err, &ae) || ae.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %v", err)
	}
}

func TestFake_MissingTrailingSlashRedirects(t *testing.T) {
	srv := fakeapi.New(t)
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodPost, strings.TrimSuffix(srv.URL(), "/")+"/vms", strings.NewReader(`{"x":1}`))
	req.Header.Set("X-API-KEY", fakeapi.APIKey)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status %d", resp.StatusCode)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || string(reqs[0].Body) != `{"x":1}` {
		t.Fatalf("recorded %+v", reqs)
	}
}

func TestFake_FailRequestsAndRespondTo(t *testing.T) {
	srv := fakeapi.New(t)
	c := newClient(t, srv)
	ctx := context.Background()
	srv.FailRequests(http.MethodGet, "vms/", 2, http.StatusServiceUnavailable)
	for i := range 2 {
		var ae *client.APIError
		if _, err := c.ListVMs(ctx); !asAPIError(err, &ae) || ae.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("request %d: want 503, got %v", i, err)
		}
	}
	if _, err := c.ListVMs(ctx); err != nil {
		t.Fatalf("injection not used up: %v", err)
	}

	uuid := createVM(t, c, "vm1", "Ubuntu-22.04", nil)
	waitVM(t, c, uuid, isActive)
	srv.FailRequests(http.MethodPost, "vms/*/action/", 1, http.StatusConflict)
	var ae *client.APIError
	if _, err := c.VMAction(ctx, uuid, client.VMActionStop); !asAPIError(err, &ae) || ae.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %v", err)
	}
	if vm, _ := c.GetVM(ctx, uuid); vm == nil || vm.VMState != client.VMStateActive || vm.TaskState != nil {
		t.Fatalf("rejected action changed the VM: %+v", vm)
	}

	srv.RespondTo(http.MethodPost, "vms/", 1, http.StatusCreated, map[string]string{"os_action": "create"})
	resp, err := c.CreateVM(ctx, client.CreateVMRequest{ProjectID: fakeapi.ProjectID, ImageName: "Ubuntu-22.04", FlavorName: "de.NBI small", VMName: "x"})
	if err != nil || resp.UUID != "" {
		t.Fatalf("canned response not served: %+v %v", resp, err)
	}
	if vms, _ := c.ListVMs(ctx); len(vms) != 1 {
		t.Fatalf("canned create must not create a VM, have %d", len(vms))
	}
}

func TestFake_IdleTaskStateAndPlaybookDoneNeverReported(t *testing.T) {
	srv := fakeapi.New(t, fakeapi.WithIdleTaskState(client.TaskStatePlaybookSuccessful), fakeapi.WithPlaybookDoneNeverReported())
	c := newClient(t, srv)
	uuid := createVM(t, c, "re", "RStudio-Ubuntu-22.04", &client.ResearchEnvironmentBackend{Template: "rstudio"})
	vm := waitVM(t, c, uuid, func(vm *client.VM) bool {
		return vm.TaskState != nil && *vm.TaskState == client.TaskStatePlaybookSuccessful
	})
	if vm.VMState != client.VMStateActive || vm.Playbook.AllDone() {
		t.Fatalf("unexpected VM: %+v", vm)
	}
	if _, err := c.VMAction(context.Background(), uuid, client.VMActionStop); err != nil {
		t.Fatal(err)
	}
	vm = waitVM(t, c, uuid, func(vm *client.VM) bool { return vm.VMState == client.VMStateStopped })
	if vm.TaskState == nil || *vm.TaskState != client.TaskStatePlaybookSuccessful {
		t.Fatalf("stopped VM task_state = %v, want the idle state", vm.TaskState)
	}
}
