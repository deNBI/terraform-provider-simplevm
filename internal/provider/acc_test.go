package provider

// Real-API acceptance tests. They only run with TF_ACC=1 (resource.Test skips
// otherwise) and need SIMPLEVM_API_KEY and SIMPLEVM_PROJECT_ID. They create
// billable resources in the configured project.
//
// Optional environment:
//
//	SIMPLEVM_ACC_FLAVOR         flavor name (default "de.NBI default")
//	SIMPLEVM_ACC_IMAGE          base image name (default: first Ubuntu image without research environment)
//	SIMPLEVM_ACC_RESENV_IMAGE   image with a research environment; TestAccVM_ResearchEnvironment skips when unset

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	accProviderConfig = "provider \"simplevm\" {}\n"
	accRandChars      = "abcdefghijklmnopqrstuvwxyz0123456789"
	accImagesData     = "\ndata \"simplevm_images\" \"all\" {}\n"
)

// testAccPreCheck fails the test when the credentials are missing. It is only
// reached with TF_ACC=1.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SIMPLEVM_API_KEY", "SIMPLEVM_PROJECT_ID"} {
		if os.Getenv(k) == "" {
			t.Fatalf("%s must be set for acceptance tests", k)
		}
	}
	if _, err := strconv.ParseInt(os.Getenv("SIMPLEVM_PROJECT_ID"), 10, 64); err != nil {
		t.Fatalf("SIMPLEVM_PROJECT_ID must be an integer: %v", err)
	}
}

func accName() string {
	return "tfacc-" + acctest.RandStringFromCharSet(6, accRandChars)
}

func accFlavor() string {
	if v := os.Getenv("SIMPLEVM_ACC_FLAVOR"); v != "" {
		return v
	}
	return "de.NBI default"
}

// accImageExpr returns an HCL expression for the base image name: the
// SIMPLEVM_ACC_IMAGE override, or the first Ubuntu image without research
// environment from data.simplevm_images.all (which the config must declare).
func accImageExpr() string {
	if v := os.Getenv("SIMPLEVM_ACC_IMAGE"); v != "" {
		return strconv.Quote(v)
	}
	return `[for i in data.simplevm_images.all.images : i.name if i.resenv_template_name == "" && strcontains(i.name, "Ubuntu")][0]`
}

// checkAttrEmptyOrUnset passes when the attribute is absent or "".
func checkAttrEmptyOrUnset(addr, attr string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[addr]
		if !ok {
			return fmt.Errorf("%s not found in state", addr)
		}
		if v := rs.Primary.Attributes[attr]; v != "" {
			return fmt.Errorf("%s.%s = %q, want empty", addr, attr, v)
		}
		return nil
	}
}

func TestAccDataSources(t *testing.T) {
	cfg := accProviderConfig + accImagesData + fmt.Sprintf(`
data "simplevm_projects" "all" {}
# No id: the key's own project, from GET projects/by-api-key/.
data "simplevm_project" "current" {}
data "simplevm_flavors" "all" {}
data "simplevm_flavor" "test" {
  name = %q
}
data "simplevm_image" "test" {
  name = %s
}
`, accFlavor(), accImageExpr())
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.simplevm_projects.all", "projects.#"),
				resource.TestCheckResourceAttrSet("data.simplevm_project.current", "id"),
				resource.TestCheckResourceAttrSet("data.simplevm_project.current", "shortname"),
				resource.TestCheckResourceAttrSet("data.simplevm_flavors.all", "flavors.#"),
				resource.TestCheckResourceAttr("data.simplevm_flavor.test", "name", accFlavor()),
				resource.TestCheckResourceAttrSet("data.simplevm_flavor.test", "vcpus"),
				resource.TestCheckResourceAttrSet("data.simplevm_image.test", "openstack_id"),
				resource.TestCheckResourceAttrSet("data.simplevm_images.all", "images.#"),
			),
		}},
	})
}

func TestAccVM_FullLifecycle(t *testing.T) {
	vmName := accName()
	volName := accName()
	volRenamed := accName()
	snapName := accName()
	const vm, vol, att, snap = "simplevm_vm.test", "simplevm_volume.test", "simplevm_volume_attachment.test", "simplevm_snapshot.test"

	// config builds the configuration; volume name and size change in place in
	// a later step, the flags add the pieces of later steps.
	config := func(power, vol string, size int, attach, snapshot bool) string {
		c := accProviderConfig + accImagesData + fmt.Sprintf(`
resource "simplevm_vm" "test" {
  name        = %q
  flavor      = %q
  image       = %s
  power_state = %q
  timeouts = {
    create = "45m"
    update = "20m"
    delete = "20m"
  }
}

resource "simplevm_volume" "test" {
  name = %q
  size = %d
}
`, vmName, accFlavor(), accImageExpr(), power, vol, size)
		if attach {
			c += `
resource "simplevm_volume_attachment" "test" {
  volume_id = simplevm_volume.test.id
  vm_id     = simplevm_vm.test.id
}
`
		}
		if snapshot {
			c += fmt.Sprintf(`
resource "simplevm_snapshot" "test" {
  vm_id       = simplevm_vm.test.id
  name        = %q
  description = "created by acceptance tests, safe to delete"
}
`, snapName)
		}
		return c
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			// Snapshots cannot be deleted through the API.
			t.Logf("this run leaves behind the snapshot %q in the project; delete it by hand (tfacc- prefix)", snapName)
		},
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config("active", volName, 1, false, false),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(vm, "id"),
				resource.TestCheckResourceAttr(vm, "name", vmName),
				resource.TestCheckResourceAttrSet(vm, "project_id"),
				resource.TestCheckResourceAttr(vm, "power_state", "active"),
				resource.TestCheckResourceAttr(vm, "vm_state", "active"),
				resource.TestCheckResourceAttrSet(vm, "openstack_id"),
				resource.TestCheckResourceAttr(vol, "name", volName),
				resource.TestCheckResourceAttr(vol, "size", "1"),
				resource.TestCheckResourceAttrSet(vol, "id"),
			),
		}, {
			// No drift and no replace loop right after create.
			Config:   config("active", volName, 1, false, false),
			PlanOnly: true,
		}, {
			ResourceName:            vm,
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"timeouts", "research_environment"},
		}, {
			ResourceName:            vol,
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"timeouts"},
		}, {
			// rename (new_name) and extend (size), in place.
			Config: config("active", volRenamed, 2, false, false),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vol, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vol, "name", volRenamed),
				resource.TestCheckResourceAttr(vol, "size", "2"),
			),
		}, {
			Config: config("active", volRenamed, 2, true, false),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(att, "id"),
				resource.TestCheckResourceAttrSet(att, "device"),
			),
		}, {
			Config: config("stopped", volRenamed, 2, true, false),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vm, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vm, "power_state", "stopped"),
				resource.TestCheckResourceAttr(vm, "vm_state", "stopped"),
			),
		}, {
			Config: config("active", volRenamed, 2, true, false),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(vm, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vm, "power_state", "active"),
				resource.TestCheckResourceAttr(vm, "vm_state", "active"),
				checkAttrEmptyOrUnset(vm, "task_state"),
			),
		}, {
			Config: config("active", volRenamed, 2, true, true),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(snap, "id"),
				resource.TestCheckResourceAttr(snap, "name", snapName),
				resource.TestCheckResourceAttrSet(snap, "status"),
			),
		}},
	})
}

func TestAccVM_ResearchEnvironment(t *testing.T) {
	image := os.Getenv("SIMPLEVM_ACC_RESENV_IMAGE")
	if image == "" {
		t.Skip("SIMPLEVM_ACC_RESENV_IMAGE not set")
	}
	vmName := accName()
	const vm = "simplevm_vm.test"
	cfg := accProviderConfig + fmt.Sprintf(`
data "simplevm_image" "resenv" {
  name = %q
}

resource "simplevm_vm" "test" {
  name   = %q
  flavor = %q
  image  = data.simplevm_image.resenv.name
  research_environment = {
    template = data.simplevm_image.resenv.resenv_template_name
  }
  timeouts = {
    create = "60m"
    delete = "20m"
  }
}
`, image, vmName, accFlavor())
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vm, "name", vmName),
				resource.TestCheckResourceAttr(vm, "vm_state", "active"),
				resource.TestCheckResourceAttrSet(vm, "research_environment.template"),
				// Set only once the playbook finished: create did not return early.
				resource.TestCheckResourceAttrSet(vm, "research_environment_url"),
			),
		}, {
			Config:   cfg,
			PlanOnly: true,
		}},
	})
}
