package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/deNBI/terraform-provider-simplevm/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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

// TestSnapshot_TransientPollErrorsAreRetried checks that a 502 while polling
// the snapshot images does not fail the create (which would leave an
// undeletable duplicate on the next apply).
func TestSnapshot_TransientPollErrorsAreRetried(t *testing.T) {
	s := newFake(t)
	s.FailRequests(http.MethodGet, "projects/1/snapshots/images/", 2, http.StatusBadGateway)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + snapshotConfigBody,
			Check:  resource.TestCheckResourceAttr(snapAddr, "status", "active"),
		}},
	})
}

// snapshotConfigWithTimeout is snapshotConfigBody with a snapshot create
// timeout.
func snapshotConfigWithTimeout(s *fakeapi.Server, create string) string {
	return fakeProviderConfig(s) + strings.Replace(snapshotConfigBody, `description = "before upgrade"`,
		fmt.Sprintf("description = \"before upgrade\"\n  timeouts = { create = %q }", create), 1)
}

func checkSnapshotPosts(s *fakeapi.Server, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if n := snapshotPosts(s); n != want {
			return fmt.Errorf("POST snapshots/ count = %d, want %d", n, want)
		}
		return nil
	}
}

// TestSnapshot_CreateTimeoutWarnsAndKeeps checks that a snapshot that is not
// active within the create timeout is kept, not tainted: no duplicate is
// created by the next apply.
func TestSnapshot_CreateTimeoutWarnsAndKeeps(t *testing.T) {
	s := newFake(t, fakeapi.WithSnapshotsStuckQueued())
	cfg := snapshotConfigWithTimeout(s, "1s")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(snapAddr, "id"),
				resource.TestCheckResourceAttr(snapAddr, "status", "queued"),
			),
		}, {
			Config:           cfg,
			PlanOnly:         true,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
		}, {
			Config: cfg,
			Check:  checkSnapshotPosts(s, 1),
		}},
	})
}

// TestSnapshot_NeverListedIsKept checks that a snapshot SimpleVM has not
// listed yet is neither an error nor removed from state on refresh.
func TestSnapshot_NeverListedIsKept(t *testing.T) {
	s := newFake(t, fakeapi.WithSnapshotsUnlisted())
	cfg := snapshotConfigWithTimeout(s, "1s")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(snapAddr, "id"),
				resource.TestCheckResourceAttr(snapAddr, "status", "pending"),
			),
		}, {
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(snapAddr, plancheck.ResourceActionNoop),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(snapAddr, "status", "pending"),
				checkSnapshotPosts(s, 1),
			),
		}},
	})
}

// TestSnapshot_SeenThenGoneIsRemoved checks that a snapshot that was listed
// and then deleted outside Terraform is removed from state.
func TestSnapshot_SeenThenGoneIsRemoved(t *testing.T) {
	s := newFake(t)
	cfg := fakeProviderConfig(s) + snapshotConfigBody
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             checkNoVMs(t, s),
		Steps: []resource.TestStep{{
			Config: cfg,
		}, {
			PreConfig:          func() { s.DeleteSnapshotOutOfBand("tf-snap") },
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(snapAddr, plancheck.ResourceActionCreate),
			}},
		}},
	})
}
