package main

import (
	"context"
	"log"

	"github.com/TrogonStack/terraform-provider-slack/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version string = "dev"

func main() {
	err := providerserver.Serve(
		context.Background(),
		provider.New(version),
		providerserver.ServeOpts{
			Address: "registry.terraform.io/trogonstack/slack",
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}
