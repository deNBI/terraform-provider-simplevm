package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/deNBI/terraform-provider-simplevm/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const attAddr = "simplevm_volume_attachment.test"

func strPtr(s string) *string { return &s }

func attachmentBase(s *fakeapi.Server) string {
	return fakeProviderConfig(s) + `
resource "simplevm_vm" "test" {
  name   = "tf-test"
  flavor = "de.NBI default"
  image  = "Ubuntu-22.04"
}

resource "simplevm_volume" "test" {
  name = "data"
  size = 10
}
`
}

const attachmentResource = `
resource "simplevm_volume_attachment" "test" {
  volume_id = simplevm_volume.test.id
  vm_id     = simplevm_vm.test.id
}
`

// attachmentID returns "<volume_id>/<vm_id>" from state.
func attachmentID(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources[attAddr]
	if !ok {
		return "", fmt.Errorf("%s not found in state", attAddr)
	}
	return rs.Primary.ID, nil
}

func TestVolumeAttachment_Lifecycle(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: attachmentBase(s) + attachmentResource,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(attAddr, "device"),
				resource.TestCheckResourceAttrSet(attAddr, "path"),
				func(st *terraform.State) error {
					vol, err := fakeClient(t, s).GetVolume(context.Background(), st.RootModule().Resources["simplevm_volume.test"].Primary.ID)
					if err != nil {
						return err
					}
					if vol.Status != client.VolumeStatusInUse {
						return fmt.Errorf("volume status = %q, want in-use", vol.Status)
					}
					return nil
				},
				func(st *terraform.State) error {
					id, err := attachmentID(st)
					if err != nil {
						return err
					}
					want := st.RootModule().Resources["simplevm_volume.test"].Primary.ID + "/" +
						st.RootModule().Resources["simplevm_vm.test"].Primary.ID
					if id != want {
						return fmt.Errorf("id = %q, want %q", id, want)
					}
					return nil
				},
			),
		}, {
			ResourceName:      attAddr,
			ImportState:       true,
			ImportStateVerify: true,
			ImportStateIdFunc: attachmentID,
			// timeouts are not stored by the API.
			ImportStateVerifyIgnore: []string{"timeouts"},
		}},
	})
}

func TestVolumeAttachment_DetachOnDestroy(t *testing.T) {
	s := newFake(t)
	var volID string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: attachmentBase(s) + attachmentResource,
			Check:  captureID("simplevm_volume.test", &volID),
		}, {
			// Removing the attachment must detach the volume but keep it.
			Config: attachmentBase(s),
			Check: func(*terraform.State) error {
				vol, err := fakeClient(t, s).GetVolume(context.Background(), volID)
				if err != nil {
					return err
				}
				if vol.Status != client.VolumeStatusAvailable || vol.VirtualMachine != nil {
					return fmt.Errorf("volume status=%q vm=%v after detach, want available and no VM", vol.Status, vol.VirtualMachine)
				}
				return nil
			},
		}},
	})
}

func TestVolumeAttachment_BadImportID(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: attachmentBase(s) + attachmentResource,
		}, {
			ResourceName:  attAddr,
			ImportState:   true,
			ImportStateId: "just-one-part",
			ExpectError:   regexp.MustCompile(`expected <volume_id>/<vm_id>`),
		}},
	})
}

func TestIsAttachedTo(t *testing.T) {
	vm := func(os *string, name string) *client.VM { return &client.VM{Name: name, OpenstackID: os} }
	vol := func(m *client.VolumeMachine) *client.Volume { return &client.Volume{VirtualMachine: m} }
	for name, tc := range map[string]struct {
		vol  *client.Volume
		vm   *client.VM
		want bool
	}{
		"not attached":            {vol(nil), vm(strPtr("a"), "n"), false},
		"same openstack id":       {vol(&client.VolumeMachine{Name: "x", OpenstackID: strPtr("a")}), vm(strPtr("a"), "n"), true},
		"other openstack id":      {vol(&client.VolumeMachine{Name: "n", OpenstackID: strPtr("b")}), vm(strPtr("a"), "n"), false},
		"vm id missing, name ok":  {vol(&client.VolumeMachine{Name: "n", OpenstackID: strPtr("b")}), vm(nil, "n"), true},
		"vol id missing, name ok": {vol(&client.VolumeMachine{Name: "n"}), vm(strPtr("a"), "n"), true},
		"ids missing, name diff":  {vol(&client.VolumeMachine{Name: "m"}), vm(nil, "n"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isAttachedTo(tc.vol, tc.vm); got != tc.want {
				t.Errorf("isAttachedTo = %v, want %v", got, tc.want)
			}
		})
	}
}
