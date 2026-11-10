package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/deNBI/terraform-provider-simplevm/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const snapAddr = "simplevm_snapshot.test"

const snapshotConfigBody = `
resource "simplevm_vm" "test" {
  name   = "tf-test"
  flavor = "de.NBI default"
  image  = "Ubuntu-22.04"
}

resource "simplevm_snapshot" "test" {
  vm_id       = simplevm_vm.test.id
  name        = "tf-snap"
  description = "before upgrade"
}
`

func snapshotPosts(s *fakeapi.Server) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == http.MethodPost && r.Path == "/portal/api/snapshots/" {
			n++
		}
	}
	return n
}

func TestSnapshot_Lifecycle(t *testing.T) {
	s := newFake(t)
	cfg := fakeProviderConfig(s) + snapshotConfigBody
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(snapAddr, "status", "active"),
					resource.TestCheckResourceAttr(snapAddr, "name", "tf-snap"),
					resource.TestCheckResourceAttr(snapAddr, "project_id", fmt.Sprint(fakeapi.ProjectID)),
					resource.TestCheckResourceAttrSet(snapAddr, "id"),
					resource.TestCheckResourceAttrSet(snapAddr, "created_at"),
				),
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
			{
				Config: cfg,
				Check: func(*terraform.State) error {
					if n := snapshotPosts(s); n != 1 {
						return fmt.Errorf("POST snapshots/ count = %d, want 1", n)
					}
					return nil
				},
			},
		},
	})
}

func TestSnapshot_DestroyWarnsAndKeepsImage(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if err := checkNoVMs(t, s)(nil); err != nil {
				return err
			}
			imgs, err := fakeClient(t, s).ListSnapshotImages(context.Background(), fakeapi.ProjectID)
			if err != nil {
				return err
			}
			if len(imgs) != 1 || imgs[0].Name != "tf-snap" {
				return fmt.Errorf("snapshot images after destroy = %+v, want the tf-snap image", imgs)
			}
			return nil
		},
		Steps: []resource.TestStep{{Config: fakeProviderConfig(s) + snapshotConfigBody}},
	})
}
