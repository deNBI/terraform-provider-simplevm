// Command terraform-provider-simplevm is the Terraform/OpenTofu provider for SimpleVM.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/deNBI/terraform-provider-simplevm/internal/provider"
)

//go:generate go tool tfplugindocs generate --provider-name simplevm

// version is set by goreleaser via ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/deNBI/simplevm",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
