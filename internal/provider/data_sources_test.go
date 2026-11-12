package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestDataSourceProject_ByAPIKey(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + `data "simplevm_project" "p" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.simplevm_project.p", "id", "1"),
				resource.TestCheckResourceAttrSet("data.simplevm_project.p", "shortname"),
			),
		}, {
			Config: fakeProviderConfig(s) + `
data "simplevm_project" "p" { id = 1 }
data "simplevm_projects" "all" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.simplevm_project.p", "shortname"),
				resource.TestCheckResourceAttr("data.simplevm_projects.all", "projects.#", "1"),
				resource.TestCheckResourceAttr("data.simplevm_projects.all", "projects.0.id", "1"),
			),
		}},
	})
}

func TestDataSourceFlavor_ByName(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + `
data "simplevm_flavor" "f" { name = "de.NBI small" }
data "simplevm_flavors" "all" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.simplevm_flavor.f", "vcpus"),
				resource.TestCheckResourceAttr("data.simplevm_flavor.f", "project_id", "1"),
				resource.TestCheckResourceAttr("data.simplevm_flavors.all", "flavors.#", "2"),
			),
		}},
	})
}

func TestDataSourceFlavor_UnknownNameListsAvailable(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      fakeProviderConfig(s) + `data "simplevm_flavor" "f" { name = "nope" }`,
			ExpectError: regexp.MustCompile(`de.NBI default,\s+de.NBI small`),
		}},
	})
}

func TestDataSourceFlavor_NoProject(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `provider "simplevm" {
  endpoint = "` + s.URL() + `"
  api_key  = "test-key"
}
data "simplevm_flavor" "f" { name = "de.NBI small" }`,
			ExpectError: regexp.MustCompile("project_id must be set on the resource or the provider"),
		}},
	})
}

func TestDataSourceImage_ResearchEnvTemplate(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + `
data "simplevm_image" "r" { name = "RStudio-Ubuntu-22.04" }
data "simplevm_image" "u" { name = "Ubuntu-22.04" }`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.simplevm_image.r", "resenv_template_name", "rstudio"),
				resource.TestCheckResourceAttr("data.simplevm_image.u", "resenv_template_name", ""),
				resource.TestCheckResourceAttr("data.simplevm_image.u", "os_distro", "ubuntu"),
				resource.TestCheckResourceAttr("data.simplevm_image.u", "tags.0", "base"),
			),
		}},
	})
}

func TestDataSourceImage_UnknownListsAvailable(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + `data "simplevm_image" "x" {
  name              = "nope"
  include_snapshots = true
}`,
			ExpectError: regexp.MustCompile(`RStudio-Ubuntu-22.04,\s+Ubuntu-22.04`),
		}},
	})
}

func TestDataSourceImages_TagFilter(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + `
data "simplevm_images" "all" {}
data "simplevm_images" "rs" { tag = "rstudio" }
data "simplevm_snapshot_images" "snaps" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.simplevm_images.all", "images.#", "2"),
				resource.TestCheckResourceAttr("data.simplevm_images.rs", "images.#", "1"),
				resource.TestCheckResourceAttr("data.simplevm_images.rs", "images.0.name", "RStudio-Ubuntu-22.04"),
				resource.TestCheckResourceAttr("data.simplevm_snapshot_images.snaps", "images.#", "0"),
			),
		}},
	})
}

func TestDataSourceVMsAndVolumes_Empty(t *testing.T) {
	s := newFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fakeProviderConfig(s) + `
data "simplevm_vms" "v" {}
data "simplevm_volumes" "v" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.simplevm_vms.v", "vms.#", "0"),
				resource.TestCheckResourceAttr("data.simplevm_volumes.v", "volumes.#", "0"),
			),
		}},
	})
}
