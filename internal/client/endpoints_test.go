package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL + "/portal/api/", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// expect describes one expected request and its canned response.
type expect struct {
	method, path string
	body         string // expected JSON body; "" means no body
	status       int
	resp         string
}

func serve(t *testing.T, e expect) *Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != e.method || r.URL.Path != "/portal/api/"+e.path {
			t.Errorf("request = %s %s, want %s /portal/api/%s", r.Method, r.URL.Path, e.method, e.path)
		}
		b, _ := io.ReadAll(r.Body)
		if e.body == "" {
			if len(b) != 0 {
				t.Errorf("unexpected body %s", b)
			}
		} else {
			var got, want any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Errorf("body not JSON: %q", b)
			}
			_ = json.Unmarshal([]byte(e.body), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %s, want %s", b, e.body)
			}
		}
		if e.status != 0 {
			w.WriteHeader(e.status)
		}
		_, _ = w.Write([]byte(e.resp))
	})
}

const vmJSON = `{"uuid":"u1","name":"vm1","openstack_id":"os1","vm_state":"active","task_state":null,
"fixed_ip":"10.0.0.5","research_environment_url":"https://x","image_name":"Ubuntu 24.04","project":7,
"flavor":{"id":3,"name":"de.NBI small","vcpus":2,"ram":4096,"root_disk":20,"ephemeral_disk":0,"gpu":0,"credits_cost_per_hour":0.5},
"playbook":{"id":1,"done":true},"created_at_date":"2025-01-01"}`

func TestEndpoints(t *testing.T) {
	ctx := context.Background()

	t.Run("ListProjects", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "projects/", resp: `[{"id":7,"shortname":"p","description":"d","cluster_activated":true,"disabled":false,"vms_limit":10,"cores_limit":20,"ram_limit":30,"volume_counter_limit":4,"volume_storage_limit":500,"is_admin":true,"is_member":true}]`})
		got, err := c.ListProjects(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := Project{ID: 7, Shortname: "p", Description: "d", ClusterActivated: true, VMsLimit: 10, CoresLimit: 20, RAMLimit: 30, VolumeCounterLimit: 4, VolumeStorageLimit: 500, IsAdmin: true, IsMember: true}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("GetProjectByAPIKey object", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "projects/by-api-key/", resp: `{"id":7,"shortname":"p"}`})
		p, err := c.GetProjectByAPIKey(ctx)
		if err != nil || p.ID != 7 || p.Shortname != "p" {
			t.Fatalf("p=%+v err=%v", p, err)
		}
	})
	t.Run("GetProjectByAPIKey list", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "projects/by-api-key/", resp: `[{"id":8,"shortname":"q"}]`})
		p, err := c.GetProjectByAPIKey(ctx)
		if err != nil || p.ID != 8 {
			t.Fatalf("p=%+v err=%v", p, err)
		}
	})
	t.Run("GetProjectByAPIKey empty list", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "projects/by-api-key/", resp: `[]`})
		if _, err := c.GetProjectByAPIKey(ctx); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("GetProject", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "projects/", resp: `{"count":2,"next":null,"previous":null,"results":[{"id":1},{"id":2,"shortname":"two"}]}`})
		p, err := c.GetProject(ctx, 2)
		if err != nil || p.Shortname != "two" {
			t.Fatalf("p=%+v err=%v", p, err)
		}
		c = serve(t, expect{method: "GET", path: "projects/", resp: `[{"id":1}]`})
		if _, err := c.GetProject(ctx, 9); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("ListFlavors", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "projects/7/flavors/", resp: `[{"id":3,"name":"small","vcpus":2,"ram":4096,"root_disk":20,"ephemeral_disk":1,"gpu":0,"credits_cost_per_hour":0.25}]`})
		got, err := c.ListFlavors(ctx, 7)
		want := Flavor{ID: 3, Name: "small", VCPUs: 2, RAM: 4096, RootDisk: 20, EphemeralDisk: 1, CreditsCostPerHour: 0.25}
		if err != nil || len(got) != 1 || got[0] != want {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	imageResp := `[{"openstack_id":"o","name":"n","title":"t","description":"d","status":"active","os_distro":"ubuntu","os_version":"24.04","tags":["a","b"],"min_ram":512,"min_disk":10,"is_snapshot":true,"resenv_template_name":"rstudio","created_at":"2025-01-01"}]`
	for name, tc := range map[string]struct {
		path string
		call func(*Client) ([]Image, error)
	}{
		"ListImages":         {"projects/7/images/", func(c *Client) ([]Image, error) { return c.ListImages(ctx, 7) }},
		"ListSnapshotImages": {"projects/7/snapshots/images/", func(c *Client) ([]Image, error) { return c.ListSnapshotImages(ctx, 7) }},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tc.call(serve(t, expect{method: "GET", path: tc.path, resp: imageResp}))
			if err != nil || len(got) != 1 {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			g := got[0]
			if g.OpenstackID != "o" || g.OSDistro != "ubuntu" || g.OSVersion != "24.04" || !reflect.DeepEqual(g.Tags, []string{"a", "b"}) ||
				g.MinRAM != 512 || g.MinDisk != 10 || !g.IsSnapshot || g.ResenvTemplateName != "rstudio" || g.CreatedAt != "2025-01-01" {
				t.Fatalf("got %+v", g)
			}
		})
	}

	t.Run("ListVMs and GetVM", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "vms/", resp: `[` + vmJSON + `]`})
		vm, err := c.GetVM(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if vm.Name != "vm1" || *vm.OpenstackID != "os1" || vm.VMState != "active" || vm.TaskState != nil || vm.FixedIP != "10.0.0.5" ||
			vm.ResearchEnvironmentURL != "https://x" || vm.ImageName != "Ubuntu 24.04" || *vm.Project != 7 ||
			vm.Flavor.Name != "de.NBI small" || vm.Flavor.CreditsCostPerHour != 0.5 || !vm.Playbook.AllDone() || *vm.CreatedAtDate != "2025-01-01" {
			t.Fatalf("vm = %+v", vm)
		}
	})

	t.Run("CreateVM", func(t *testing.T) {
		c := serve(t, expect{method: "POST", path: "vms/",
			body: `{"project_id":7,"image_name":"img","flavor_name":"fl","vm_name":"n","research_environment_backend":{"create_only":false,"template":"rstudio","user_path":"rstudio"}}`,
			resp: `{"uuid":"u1","openstack_id":"o1","os_action":"create","vm_state":"building","task_state":"preparing"}`})
		got, err := c.CreateVM(ctx, CreateVMRequest{ProjectID: 7, ImageName: "img", FlavorName: "fl", VMName: "n",
			ResearchEnvironment: &ResearchEnvironmentBackend{Template: "rstudio", UserPath: "rstudio"}})
		want := VMActionResponse{UUID: "u1", OpenstackID: "o1", OSAction: "create", VMState: "building", TaskState: "preparing"}
		if err != nil || *got != want {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})
	t.Run("CreateVM without research environment", func(t *testing.T) {
		c := serve(t, expect{method: "POST", path: "vms/", body: `{"project_id":7,"image_name":"img","flavor_name":"fl","vm_name":"n"}`, resp: `{"uuid":"u1"}`})
		if _, err := c.CreateVM(ctx, CreateVMRequest{ProjectID: 7, ImageName: "img", FlavorName: "fl", VMName: "n"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("DeleteVM", func(t *testing.T) {
		c := serve(t, expect{method: "DELETE", path: "vms/u1/", status: http.StatusNoContent})
		if err := c.DeleteVM(ctx, "u1"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("DeleteVM 404 returned as is", func(t *testing.T) {
		c := serve(t, expect{method: "DELETE", path: "vms/u1/", status: http.StatusNotFound, resp: `{"detail":"nope"}`})
		if err := c.DeleteVM(ctx, "u1"); !IsNotFound(err) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("VMAction", func(t *testing.T) {
		c := serve(t, expect{method: "POST", path: "vms/u1/action/", body: `{"os_action":"stop"}`,
			resp: `{"uuid":"u1","openstack_id":"o1","os_action":"stop","vm_state":"active","task_state":"powering-off"}`})
		got, err := c.VMAction(ctx, "u1", VMActionStop)
		if err != nil || got.OSAction != "stop" || got.TaskState != "powering-off" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	volJSON := `{"uuid":"v1","name":"vol","device":"/dev/vdb","path":"/vol","status":"in-use","storage":10,"openstack_id":"ov1","project_id":"7",
"virtual_machine":{"name":"vm1","openstack_id":"os1","vm_state":"active"}}`

	t.Run("ListVolumes", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "volumes/by-api-key/", resp: `[` + volJSON + `]`})
		got, err := c.ListVolumes(ctx)
		if err != nil || len(got) != 1 {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		v := got[0]
		if v.UUID != "v1" || v.Device != "/dev/vdb" || v.Path != "/vol" || v.Status != "in-use" || v.Storage != 10 || *v.OpenstackID != "ov1" ||
			v.ProjectID.String() != "7" || v.VirtualMachine == nil || v.VirtualMachine.Name != "vm1" || v.VirtualMachine.VMState != "active" {
			t.Fatalf("v = %+v", v)
		}
	})

	t.Run("GetVolume", func(t *testing.T) {
		c := serve(t, expect{method: "GET", path: "volumes/v1/", resp: `{"uuid":"v1","status":"available","project_id":7,"virtual_machine":null}`})
		v, err := c.GetVolume(ctx, "v1")
		if err != nil || v.Status != "available" || v.ProjectID.String() != "7" || v.VirtualMachine != nil {
			t.Fatalf("v=%+v err=%v", v, err)
		}
		c = serve(t, expect{method: "GET", path: "volumes/v1/", resp: `{"uuid":"v1","status":"deleted"}`})
		if _, err := c.GetVolume(ctx, "v1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("CreateVolume", func(t *testing.T) {
		c := serve(t, expect{method: "POST", path: "volumes/", body: `{"project_id":7,"storage":10,"name":"vol"}`,
			resp: `{"uuid":"v1","openstack_id":"ov1","os_action":"create","volume_status":"creating","name":"vol","size":10}`})
		got, err := c.CreateVolume(ctx, CreateVolumeRequest{ProjectID: 7, Storage: 10, Name: "vol"})
		want := VolumeActionResponse{UUID: "v1", OpenstackID: "ov1", OSAction: "create", VolumeStatus: "creating", Name: "vol", Size: 10}
		if err != nil || *got != want {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("DeleteVolume", func(t *testing.T) {
		c := serve(t, expect{method: "DELETE", path: "volumes/v1/", status: http.StatusNoContent})
		if err := c.DeleteVolume(ctx, "v1"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("VolumeAction", func(t *testing.T) {
		c := serve(t, expect{method: "POST", path: "volumes/v1/action/", body: `{"os_action":"attach","vm_uuid":"u1"}`, resp: volJSON})
		v, err := c.VolumeAction(ctx, "v1", VolumeActionRequest{OSAction: "attach", VMUUID: "u1"})
		if err != nil || v.VirtualMachine == nil || v.VirtualMachine.Name != "vm1" {
			t.Fatalf("v=%+v err=%v", v, err)
		}
		c = serve(t, expect{method: "POST", path: "volumes/v1/action/", body: `{"os_action":"extend","size":20}`, resp: volJSON})
		if _, err := c.VolumeAction(ctx, "v1", VolumeActionRequest{OSAction: "extend", Size: 20}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("CreateSnapshot", func(t *testing.T) {
		c := serve(t, expect{method: "POST", path: "snapshots/", body: `{"name":"s","description":"d","vm_uuid":"u1"}`,
			resp: `{"name":"s","openstack_id":"os","status":"queued","description":"d","created_at":"2025-01-01","project_id":7}`})
		got, err := c.CreateSnapshot(ctx, CreateSnapshotRequest{Name: "s", Description: "d", VMUUID: "u1"})
		want := Snapshot{Name: "s", OpenstackID: "os", Status: "queued", Description: "d", CreatedAt: "2025-01-01", ProjectID: 7}
		if err != nil || *got != want {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})
}

func TestPlaybooks_Unmarshal(t *testing.T) {
	for name, tc := range map[string]struct {
		in      string
		want    int
		allDone bool
	}{
		"null":        {`null`, 0, true},
		"object":      {`{"id":1,"done":false}`, 1, false},
		"array":       {`[{"id":1,"done":true},{"id":2,"done":false}]`, 2, false},
		"array done":  {`[{"id":1,"done":true}]`, 1, true},
		"empty array": {`[]`, 0, true},
	} {
		t.Run(name, func(t *testing.T) {
			var v struct {
				P Playbooks `json:"playbook"`
			}
			if err := json.Unmarshal([]byte(`{"playbook":`+tc.in+`}`), &v); err != nil {
				t.Fatal(err)
			}
			if len(v.P) != tc.want || v.P.AllDone() != tc.allDone {
				t.Fatalf("p=%+v", v.P)
			}
		})
	}
	var v struct {
		P Playbooks `json:"playbook"`
	}
	if err := json.Unmarshal([]byte(`{"playbook":"x"}`), &v); err == nil {
		t.Fatal("expected error for string")
	}
}

func TestVolume_ProjectIDNumberOrString(t *testing.T) {
	for _, in := range []string{`{"project_id":7}`, `{"project_id":"7"}`} {
		var v Volume
		if err := json.Unmarshal([]byte(in), &v); err != nil || v.ProjectID.String() != "7" {
			t.Fatalf("%s: v=%+v err=%v", in, v, err)
		}
	}
}

func TestGetVM_NotFoundAndDeleted(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"uuid":"a","vm_state":"active"},{"uuid":"d","vm_state":"deleted"}]`))
	})
	ctx := context.Background()
	if vm, err := c.GetVM(ctx, "a"); err != nil || vm.UUID != "a" {
		t.Fatalf("vm=%+v err=%v", vm, err)
	}
	for _, id := range []string{"missing", "d"} {
		if _, err := c.GetVM(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: err=%v", id, err)
		}
	}
}
