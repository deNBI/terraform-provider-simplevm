package provider

import (
	"context"
	"fmt"
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
				resource.TestCheckResourceAttrSet(vmAddr, "id"),
				resource.TestCheckResourceAttrSet(vmAddr, "openstack_id"),
				resource.TestCheckResourceAttrSet(vmAddr, "fixed_ip"),
				resource.TestCheckResourceAttrSet(vmAddr, "created_at"),
				resource.TestCheckNoResourceAttr(vmAddr, "research_environment"),
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
			ResourceName:            vmAddr,
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"timeouts", "research_environment"},
		}},
	})
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
