package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/deNBI/terraform-provider-simplevm/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const volAddr = "simplevm_volume.test"

func volumeConfig(s *fakeapi.Server, name string, size int) string {
	return fakeProviderConfig(s) + fmt.Sprintf(`
resource "simplevm_volume" "test" {
  name = %q
  size = %d
}
`, name, size)
}

// checkNoVolumes is a CheckDestroy verifying that the fake has no volumes left.
func checkNoVolumes(t *testing.T, s *fakeapi.Server) resource.TestCheckFunc {
	return func(*terraform.State) error {
		vols, err := fakeClient(t, s).ListVolumes(context.Background())
		if err != nil {
			return err
		}
		// The fake's list endpoint does not advance volumes, so deleted ones
		// stay listed until read again.
		var left []client.Volume
		for _, v := range vols {
			if v.Status != client.VolumeStatusDeleted {
				left = append(left, v)
			}
		}
		if len(left) != 0 {
			return fmt.Errorf("%d volume(s) left after destroy: %+v", len(left), left)
		}
		return nil
	}
}

func TestVolume_Lifecycle(t *testing.T) {
	s := newFake(t)
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVolumes(t, s),
		Steps: []resource.TestStep{{
			Config: volumeConfig(s, "data", 10),
			Check: resource.ComposeAggregateTestCheckFunc(
				captureID(volAddr, &id),
				resource.TestCheckResourceAttr(volAddr, "name", "data"),
				resource.TestCheckResourceAttr(volAddr, "size", "10"),
				resource.TestCheckResourceAttr(volAddr, "status", "available"),
				resource.TestCheckResourceAttr(volAddr, "project_id", "1"),
				resource.TestCheckResourceAttrSet(volAddr, "openstack_id"),
			),
		}, {
			Config: volumeConfig(s, "data-renamed", 20),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(volAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(volAddr, "name", "data-renamed"),
				resource.TestCheckResourceAttr(volAddr, "size", "20"),
				resource.TestCheckResourceAttr(volAddr, "status", "available"),
				func(st *terraform.State) error {
					if got := st.RootModule().Resources[volAddr].Primary.ID; got != id {
						return fmt.Errorf("volume was replaced: id %s -> %s", id, got)
					}
					return nil
				},
			),
		}, {
			ResourceName:            volAddr,
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"timeouts"},
		}},
	})
}

func TestVolume_ShrinkReplaces(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVolumes(t, s),
		Steps: []resource.TestStep{{
			Config: volumeConfig(s, "data", 20),
		}, {
			Config: volumeConfig(s, "data", 10),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(volAddr, plancheck.ResourceActionReplace),
			}},
			Check: resource.TestCheckResourceAttr(volAddr, "size", "10"),
		}},
	})
}

func TestVolume_Disappears(t *testing.T) {
	s := newFake(t)
	var id string
	cfg := volumeConfig(s, "data", 10)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVolumes(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check:  captureID(volAddr, &id),
		}, {
			PreConfig: func() { s.DeleteVolumeOutOfBand(id) },
			Config:    cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(volAddr, plancheck.ResourceActionCreate),
			}},
			Check: resource.TestCheckResourceAttr(volAddr, "status", "available"),
		}},
	})
}

func TestVolumeSizeShrinks(t *testing.T) {
	for name, tc := range map[string]struct {
		plan, state types.Int64
		want        bool
	}{
		"unknown plan": {types.Int64Unknown(), types.Int64Value(20), false},
		"null plan":    {types.Int64Null(), types.Int64Value(20), false},
		"grow":         {types.Int64Value(30), types.Int64Value(20), false},
		"equal":        {types.Int64Value(20), types.Int64Value(20), false},
		"shrink":       {types.Int64Value(10), types.Int64Value(20), true},
	} {
		t.Run(name, func(t *testing.T) {
			got := sizeShrinks(planmodifier.Int64Request{PlanValue: tc.plan, StateValue: tc.state})
			if got != tc.want {
				t.Errorf("sizeShrinks = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestVolume_RenameOnly checks that a rename is read back from SimpleVM
// rather than taken from the action response, which may predate it.
func TestVolume_RenameOnly(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVolumes(t, s),
		Steps: []resource.TestStep{{
			Config: volumeConfig(s, "data", 10),
		}, {
			Config: volumeConfig(s, "data-renamed", 10),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(volAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(volAddr, "name", "data-renamed"),
				resource.TestCheckResourceAttr(volAddr, "size", "10"),
				resource.TestCheckResourceAttr(volAddr, "status", "available"),
			),
		}},
	})
}

// TestVolume_CreateWithoutUUIDFails checks that a create response without
// uuid is an error and nothing with an empty id is saved.
func TestVolume_CreateWithoutUUIDFails(t *testing.T) {
	s := newFake(t)
	s.RespondTo(http.MethodPost, "volumes/", 1, http.StatusCreated, map[string]string{"os_action": "create"})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      volumeConfig(s, "data", 10),
			ExpectError: regexp.MustCompile(`returned no volume uuid`),
		}},
	})
}

func TestVolume_NameLength(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      volumeConfig(s, strings.Repeat("a", 33), 10),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`string length must be between 1 and 32`),
		}},
	})
}

func TestUntilVolume_FailureStates(t *testing.T) {
	for _, st := range []string{"error", "creation_failed", "error_extending", "deleting_failed", "error_deleting", "error_restoring"} {
		if _, err := untilVolume(client.VolumeStatusAvailable, 0)(&client.Volume{Status: st}); err == nil {
			t.Errorf("status %q: want failure", st)
		}
	}
}
