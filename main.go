// Package main starts the ASC Terraform provider.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/provider"
)

var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with debugger support")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/nl-ams-dspc/asc",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
