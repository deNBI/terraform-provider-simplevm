package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/deNBI/terraform-provider-simplevm/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const vmAddr = "simplevm_vm.test"

// vmConfig returns a provider block plus a simplevm_vm.test resource using the
// given image and extra resource arguments.
func vmConfig(s *fakeapi.Server, image, extra string) string {
	return fakeProviderConfig(s) + fmt.Sprintf(`
resource "simplevm_vm" "test" {
  name   = "tf-test"
  flavor = "de.NBI default"
  image  = %q
%s
}
`, image, extra)
}

// captureID stores the id of the named resource from state into id.
func captureID(name string, id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not found in state", name)
		}
		*id = rs.Primary.ID
		return nil
	}
}

// checkSSHPortOfFixedIP checks that the ssh_port attribute (under prefix,
// for example "vms.0.") is the fake's port for the fixed_ip next to it:
// 30000 plus the last two bytes of the address.
func checkSSHPortOfFixedIP(name, prefix string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not found in state", name)
		}
		ip, err := netip.ParseAddr(rs.Primary.Attributes[prefix+"fixed_ip"])
		if err != nil {
			return err
		}
		b := ip.As4()
		want := fmt.Sprint(30000 + int(b[2])*256 + int(b[3]))
		if got := rs.Primary.Attributes[prefix+"ssh_port"]; got != want {
			return fmt.Errorf("%s: %sssh_port = %q, want %s for fixed_ip %s", name, prefix, got, want, ip)
		}
		return nil
	}
}

// fakeClient returns an API client talking to the fake server.
func fakeClient(t *testing.T, s *fakeapi.Server) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Endpoint: s.URL(), APIKey: fakeapi.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// checkNoVMs is a CheckDestroy verifying that the fake has no VMs left.
func checkNoVMs(t *testing.T, s *fakeapi.Server) resource.TestCheckFunc {
	return func(*terraform.State) error {
		vms, err := fakeClient(t, s).ListVMs(context.Background())
		if err != nil {
			return err
		}
		if len(vms) != 0 {
			return fmt.Errorf("%d VM(s) left after destroy: %+v", len(vms), vms)
		}
		return nil
	}
}

// actionRequests counts recorded POST vms/{uuid}/action/ requests.
func actionRequests(s *fakeapi.Server) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/action/") {
			n++
		}
	}
	return n
}

func TestVM_BaseImage(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "Ubuntu-22.04", ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
				resource.TestCheckResourceAttr(vmAddr, "power_state", "active"),
				resource.TestCheckResourceAttr(vmAddr, "project_id", "1"),
				// SimpleVM appends a suffix; name keeps the configured value.
				resource.TestCheckResourceAttr(vmAddr, "name", "tf-test"),
				resource.TestCheckResourceAttr(vmAddr, "full_name", "tf-test-a0001"),
				resource.TestCheckResourceAttrSet(vmAddr, "id"),
				resource.TestCheckResourceAttrSet(vmAddr, "openstack_id"),
				resource.TestCheckResourceAttrSet(vmAddr, "fixed_ip"),
				resource.TestCheckResourceAttrSet(vmAddr, "created_at"),
				resource.TestCheckNoResourceAttr(vmAddr, "research_environment"),
				// From the "SSH" access mode: gateway host and the VM's port.
				resource.TestCheckResourceAttr(vmAddr, "ssh_host", fakeapi.SSHGateway),
				checkSSHPortOfFixedIP(vmAddr, ""),
				resource.TestCheckResourceAttr(vmAddr, "ssh_user", "ubuntu"),
			),
		}, {
			Config: vmConfig(s, "Ubuntu-22.04", `  power_state = "stopped"`),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "stopped"),
				resource.TestCheckResourceAttr(vmAddr, "power_state", "stopped"),
			),
		}, {
			Config: vmConfig(s, "Ubuntu-22.04", `  power_state = "active"`),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
				resource.TestCheckResourceAttr(vmAddr, "power_state", "active"),
			),
		}, {
			ResourceName:      vmAddr,
			ImportState:       true,
			ImportStateVerify: true,
			// An import only knows the full name; see TestVM_ImportThenApplyKeepsVM.
			ImportStateVerifyIgnore: []string{"timeouts", "research_environment", "name"},
		}},
	})
}

// TestVM_ImportThenApplyKeepsVM checks that importing a VM, which sets name
// to the suffixed full_name, followed by a config with the short name
// updates name in place instead of replacing the VM.
func TestVM_ImportThenApplyKeepsVM(t *testing.T) {
	s := newFake(t)
	created, err := fakeClient(t, s).CreateVM(context.Background(), client.CreateVMRequest{
		ProjectID: fakeapi.ProjectID, ImageName: "Ubuntu-22.04", FlavorName: "de.NBI default", VMName: "tf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := vmConfig(s, "Ubuntu-22.04", "") + fmt.Sprintf(`
import {
  to = simplevm_vm.test
  id = %q
}
`, created.UUID)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "id", created.UUID),
				resource.TestCheckResourceAttr(vmAddr, "name", "tf-test"),
				resource.TestCheckResourceAttr(vmAddr, "full_name", "tf-test-a0001"),
			),
		}, {
			Config:   cfg,
			PlanOnly: true,
		}},
	})
}

// TestVM_RenameReplaces checks that a real name change still replaces the VM.
func TestVM_RenameReplaces(t *testing.T) {
	s := newFake(t)
	cfg := vmConfig(s, "Ubuntu-22.04", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
		}, {
			Config: strings.Replace(cfg, `"tf-test"`, `"tf-test-b"`, 1),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			Check: resource.TestCheckResourceAttr(vmAddr, "name", "tf-test-b"),
		}},
	})
}

func TestNameMatches(t *testing.T) {
	for _, tc := range []struct {
		name, actual string
		want         bool
	}{
		{"vm", "vm", true},
		{"vm", "vm-faaef", true},
		{"my-vm", "my-vm-faaef", true},
		{"vm", "vm-", false},
		{"vm", "vm-rstudio-faaef", false},
		{"vm", "vmx-faaef", false},
		{"vm", "other", false},
	} {
		if got := nameMatches(tc.name, tc.actual); got != tc.want {
			t.Errorf("nameMatches(%q, %q) = %v, want %v", tc.name, tc.actual, got, tc.want)
		}
	}
}

func TestVM_ResearchEnvironmentWaitsForPlaybook(t *testing.T) {
	s := newFake(t)
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "RStudio-Ubuntu-22.04", `  research_environment = { template = "rstudio" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				captureID(vmAddr, &id),
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
				resource.TestCheckResourceAttr(vmAddr, "research_environment.template", "rstudio"),
				resource.TestCheckResourceAttr(vmAddr, "research_environment.user_path", ""),
				resource.TestCheckResourceAttr(vmAddr, "research_environment.create_only", "false"),
				resource.TestCheckResourceAttrSet(vmAddr, "research_environment_url"),
				func(*terraform.State) error {
					vm, err := fakeClient(t, s).GetVM(context.Background(), id)
					if err != nil {
						return err
					}
					if len(vm.Playbook) == 0 || !vm.Playbook.AllDone() {
						return fmt.Errorf("playbook not done: %+v", vm.Playbook)
					}
					return nil
				},
				func(*terraform.State) error {
					want := `"research_environment_backend":{"create_only":false,"template":"rstudio","user_path":""}`
					for _, r := range s.Requests() {
						if r.Method == "POST" && r.Path == "/portal/api/vms/" {
							if !strings.Contains(string(r.Body), want) {
								return fmt.Errorf("create body %s does not contain %s", r.Body, want)
							}
							return nil
						}
					}
					return fmt.Errorf("no create request recorded")
				},
			),
		}},
	})
}

func TestVM_PlaybookFailed(t *testing.T) {
	s := newFake(t, fakeapi.WithPlaybookFailure())
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config:      vmConfig(s, "RStudio-Ubuntu-22.04", `  research_environment = { template = "rstudio" }`),
			ExpectError: regexp.MustCompile("playbook_failed"),
		}, {
			// The failed VM was saved (tainted), so it is replaced rather than orphaned.
			Config:             vmConfig(s, "RStudio-Ubuntu-22.04", `  research_environment = { template = "rstudio" }`),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionReplace),
			}},
		}},
	})
}

func TestVM_CreateStopped(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "Ubuntu-22.04", `  power_state = "stopped"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "stopped"),
				resource.TestCheckResourceAttr(vmAddr, "power_state", "stopped"),
			),
		}},
	})
}

func TestVM_TransientStateNoDrift(t *testing.T) {
	s := newFake(t)
	var id string
	var actionsAfterCreate int
	cfg := vmConfig(s, "Ubuntu-22.04", "")
	emptyPlan := resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check:  captureID(vmAddr, &id),
		}, {
			PreConfig: func() {
				actionsAfterCreate = actionRequests(s)
				s.SetVMState(id, "active", "rebooting")
			},
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: false,
			ConfigPlanChecks:   emptyPlan,
		}, {
			PreConfig:          func() { s.SetVMState(id, "building", "") },
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: false,
			ConfigPlanChecks:   emptyPlan,
		}, {
			PreConfig: func() {
				if n := actionRequests(s); n != actionsAfterCreate {
					t.Errorf("transient states triggered %d VM action request(s)", n-actionsAfterCreate)
				}
			},
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: false,
		}},
	})
}

func TestVM_Disappears(t *testing.T) {
	s := newFake(t)
	var id string
	cfg := vmConfig(s, "Ubuntu-22.04", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check:  captureID(vmAddr, &id),
		}, {
			PreConfig: func() { s.DeleteVMOutOfBand(id) },
			Config:    cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionCreate),
			}},
			Check: resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
		}},
	})
}

func TestVM_ForbiddenIsError(t *testing.T) {
	s := newFake(t, fakeapi.WithForbidden())
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      vmConfig(s, "Ubuntu-22.04", ""),
			ExpectError: regexp.MustCompile("scope"),
		}},
	})
}

func TestVM_ReplaceOnImageChange(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "Ubuntu-22.04", ""),
		}, {
			Config: vmConfig(s, "RStudio-Ubuntu-22.04", ""),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionReplace),
			}},
			Check: resource.TestCheckResourceAttr(vmAddr, "image", "RStudio-Ubuntu-22.04"),
		}},
	})
}

// TestVM_WaitTimeoutReportsState checks that a wait that runs out of time
// names the last observed vm_state and task_state.
func TestVM_WaitTimeoutReportsState(t *testing.T) {
	s := newFake(t)
	c := fakeClient(t, s)
	ctx := context.Background()
	created, err := c.CreateVM(ctx, client.CreateVMRequest{
		ProjectID: fakeapi.ProjectID, ImageName: "Ubuntu-22.04", FlavorName: "de.NBI default", VMName: "stuck",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetVMState(created.UUID, "building", "spawning")

	r := &vmResource{pd: &ProviderData{Client: c, PollInterval: 5 * time.Millisecond}}
	wctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = r.waitVM(wctx, created.UUID, func(vm *client.VM) (bool, error) {
		return vm.VMState == client.VMStateActive, nil
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	for _, want := range []string{"vm_state=building", "task_state=spawning"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// TestVM_TransientPollErrorsAreRetried checks that a few 5xx answers while
// polling do not fail the create.
func TestVM_TransientPollErrorsAreRetried(t *testing.T) {
	s := newFake(t)
	// No GET vms/{uuid}/ happens before the create's wait starts.
	s.FailRequests(http.MethodGet, "vms/*/", 2, http.StatusServiceUnavailable)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "Ubuntu-22.04", ""),
			Check:  resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
		}},
	})
}

// TestVM_TransientErrorsOnRefreshAndDestroyAreRetried checks that a refresh
// and a destroy survive single 502/504 answers of the SimpleVM gateway,
// which used to fail plan, apply and destroy.
func TestVM_TransientErrorsOnRefreshAndDestroyAreRetried(t *testing.T) {
	s := newFake(t)
	cfg := vmConfig(s, "Ubuntu-22.04", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
		}, {
			PreConfig: func() { s.FailRequests(http.MethodGet, "vms/*/", 2, http.StatusGatewayTimeout) },
			Config:    cfg,
			PlanOnly:  true,
		}, {
			PreConfig: func() { s.FailRequests(http.MethodDelete, "vms/*/", 1, http.StatusBadGateway) },
			Config:    fakeProviderConfig(s),
		}},
	})
}

// TestVM_TooManyTransientPollErrorsFail checks that polling gives up after
// more than client.MaxConsecutiveTransientErrors polls in a row failed with
// a transient error, each after client.DefaultMaxAttempts requests.
func TestVM_TooManyTransientPollErrorsFail(t *testing.T) {
	s := newFake(t)
	s.FailRequests(http.MethodGet, "vms/*/", (client.MaxConsecutiveTransientErrors+1)*client.DefaultMaxAttempts, http.StatusServiceUnavailable)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config:      vmConfig(s, "Ubuntu-22.04", ""),
			ExpectError: regexp.MustCompile(`HTTP 503`),
		}},
	})
}

// TestVM_CreateStopFailureWarns checks that a VM that was created but could
// not be stopped is kept (not tainted): the next plan stops it in place.
func TestVM_CreateStopFailureWarns(t *testing.T) {
	s := newFake(t)
	s.FailRequests(http.MethodPost, "vms/*/action/", 1, http.StatusConflict)
	cfg := vmConfig(s, "Ubuntu-22.04", `  power_state = "stopped"`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
				resource.TestCheckResourceAttr(vmAddr, "power_state", "stopped"),
			),
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate),
			}},
		}, {
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.TestCheckResourceAttr(vmAddr, "vm_state", "stopped"),
		}},
	})
}

// TestVM_TimeoutsOnlyUpdateLeavesPowerAlone checks that changing only
// timeouts sends no stop/resume, even while the VM is in a transient state.
func TestVM_TimeoutsOnlyUpdateLeavesPowerAlone(t *testing.T) {
	s := newFake(t)
	var id string
	var actions int
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "Ubuntu-22.04", ""),
			Check:  captureID(vmAddr, &id),
		}, {
			PreConfig: func() {
				s.SetVMState(id, client.VMStateBuilding, "")
				actions = actionRequests(s)
			},
			Config: vmConfig(s, "Ubuntu-22.04", `  timeouts = { update = "5m" }`),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "timeouts.update", "5m"),
				resource.TestCheckResourceAttr(vmAddr, "power_state", "active"),
				func(*terraform.State) error {
					if n := actionRequests(s) - actions; n != 0 {
						return fmt.Errorf("timeouts-only update sent %d VM action request(s)", n)
					}
					return nil
				},
			),
		}},
	})
}

// TestVM_PowerChangeWithIdleTaskState checks that an idle, non-empty
// task_state does not keep a stop from completing.
func TestVM_PowerChangeWithIdleTaskState(t *testing.T) {
	s := newFake(t, fakeapi.WithIdleTaskState("checking_status"))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "Ubuntu-22.04", ""),
		}, {
			Config: vmConfig(s, "Ubuntu-22.04", `  power_state = "stopped"
  timeouts = { update = "3s" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "stopped"),
				resource.TestCheckResourceAttr(vmAddr, "task_state", "checking_status"),
			),
		}},
	})
}

func TestUntilPower(t *testing.T) {
	vm := func(state, task string) *client.VM {
		v := &client.VM{VMState: state}
		if task != "" {
			v.TaskState = &task
		}
		return v
	}
	for name, tc := range map[string]struct {
		vm   *client.VM
		want bool
	}{
		"target, idle":              {vm(client.VMStateStopped, ""), true},
		"target, checking_status":   {vm(client.VMStateStopped, "checking_status"), true},
		"target, playbook_success":  {vm(client.VMStateStopped, client.TaskStatePlaybookSuccessful), true},
		"target, powering-off":      {vm(client.VMStateStopped, "powering-off"), false},
		"target, rebooting":         {vm(client.VMStateStopped, "rebooting"), false},
		"other state, idle":         {vm(client.VMStateActive, ""), false},
		"other state, powering-off": {vm(client.VMStateActive, "powering-off"), false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := untilPower(client.VMStateStopped)(tc.vm)
			if err != nil || got != tc.want {
				t.Errorf("untilPower = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// TestVM_ResearchEnvironmentPlaybookSuccessful checks that task_state
// playbook_successful completes the create even when playbook[].done is never
// reported.
func TestVM_ResearchEnvironmentPlaybookSuccessful(t *testing.T) {
	s := newFake(t, fakeapi.WithIdleTaskState(client.TaskStatePlaybookSuccessful), fakeapi.WithPlaybookDoneNeverReported())
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: vmConfig(s, "RStudio-Ubuntu-22.04", `  research_environment = { template = "rstudio" }
  timeouts = { create = "3s" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "vm_state", "active"),
				resource.TestCheckResourceAttr(vmAddr, "task_state", client.TaskStatePlaybookSuccessful),
			),
		}},
	})
}

// TestVM_CreateWithoutUUIDFails checks that a create response without uuid
// is an error and nothing with an empty id is saved.
func TestVM_CreateWithoutUUIDFails(t *testing.T) {
	s := newFake(t)
	s.RespondTo(http.MethodPost, "vms/", 1, http.StatusCreated, map[string]string{"os_action": "create", "vm_state": "planned"})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      vmConfig(s, "Ubuntu-22.04", ""),
			ExpectError: regexp.MustCompile(`returned no VM uuid`),
		}},
	})
}

func TestVM_NameLength(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + fmt.Sprintf(`
resource "simplevm_vm" "test" {
  name   = %q
  flavor = "de.NBI default"
  image  = "Ubuntu-22.04"
}
`, strings.Repeat("a", 101)),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`string length must be between 1 and 100`),
		}},
	})
}

// TestVM_NotFoundStateIsGone checks that vm_state not_found removes the VM
// from state like a deleted one.
func TestVM_NotFoundStateIsGone(t *testing.T) {
	s := newFake(t)
	var id string
	cfg := vmConfig(s, "Ubuntu-22.04", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: cfg,
			Check:  captureID(vmAddr, &id),
		}, {
			PreConfig:          func() { s.SetVMState(id, "not_found", "") },
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionCreate),
			}},
		}},
	})
}
