// Package provider implements the ASC Terraform provider.
package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
	groupdatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/group"
	roledatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/role"
	skudatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/sku"
	subnetdatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/subnet"
	virtualmachinedatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/virtualmachine"
	vmgroupdatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/vmgroup"
	volumedatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/volume"
	vpcdatasource "github.com/nl-ams-dspc/terraform-provider-asc/internal/datasources/vpc"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/group"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/role"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/subnet"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/virtualmachine"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/vmgroup"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/volume"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/volumeattachment"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/vpc"
)

var _ provider.Provider = &ASCProvider{}

// ASCProvider manages infrastructure and UAM resources through the ASC gateway.
type ASCProvider struct {
	version string
}

type providerModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	Namespace types.String `tfsdk:"namespace"`
	AuthURL   types.String `tfsdk:"auth_url"`
	Org       types.String `tfsdk:"org"`
	Username  types.String `tfsdk:"username"`
	Password  types.String `tfsdk:"password"`
	Timeout   types.Int64  `tfsdk:"timeout"`
}

// New returns a provider factory for the Terraform protocol server.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ASCProvider{version: version}
	}
}

// Metadata declares the stable Terraform provider identity.
func (p *ASCProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "asc"
	resp.Version = p.version
}

func (p *ASCProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage ASC infrastructure through infra-gateway using OAuth client credentials.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Description: "ASC API endpoint. May be set with ASC_ENDPOINT.",
				Optional:    true,
			},
			"namespace": schema.StringAttribute{
				Description: "Tenant namespace sent to infra-gateway as X-Tenant. May be set with ASC_NAMESPACE.",
				Optional:    true,
			},
			"auth_url": schema.StringAttribute{
				Description: "Authentication endpoint. May be set with ASC_AUTH_URL.",
				Optional:    true,
			},
			"org": schema.StringAttribute{
				Description: "Authentication realm or organization. May be set with ASC_ORG.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: "OAuth client ID. May be set with ASC_USERNAME.",
				Optional:    true,
			},
			"password": schema.StringAttribute{
				Description: "OAuth client secret. May be set with ASC_PASSWORD.",
				Optional:    true,
				Sensitive:   true,
			},
			"timeout": schema.Int64Attribute{
				Description: "HTTP timeout in seconds. May be set with ASC_TIMEOUT; defaults to 30.",
				Optional:    true,
			},
		},
	}
}

// Configure validates provider settings and builds one shared authenticated
// runtime for all resources.
func (p *ASCProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	values := []types.String{model.Endpoint, model.Namespace, model.AuthURL, model.Org, model.Username, model.Password}
	for _, value := range values {
		if value.IsUnknown() {
			resp.Diagnostics.AddError("Unknown Provider Configuration", "Provider settings must be known before ASC resources can be configured.")
			return
		}
	}
	if model.Timeout.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Provider Timeout", "The provider timeout must be known before ASC resources can be configured.")
		return
	}

	timeout, err := resolveTimeout(model.Timeout)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Provider Timeout", err.Error())
		return
	}

	config, err := client.NewConfig(
		resolveString(model.Endpoint, "ASC_ENDPOINT"),
		resolveString(model.Namespace, "ASC_NAMESPACE"),
		resolveString(model.AuthURL, "ASC_AUTH_URL"),
		resolveString(model.Org, "ASC_ORG"),
		resolveString(model.Username, "ASC_USERNAME"),
		resolveString(model.Password, "ASC_PASSWORD"),
		timeout,
	)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Provider Configuration", err.Error())
		return
	}

	runtime := client.NewRuntime(config)
	resp.ResourceData = runtime
	resp.DataSourceData = runtime
}

func resolveString(value types.String, environment string) string {
	if !value.IsNull() && strings.TrimSpace(value.ValueString()) != "" {
		return value.ValueString()
	}
	return os.Getenv(environment)
}

func resolveTimeout(value types.Int64) (time.Duration, error) {
	if !value.IsNull() {
		if value.ValueInt64() <= 0 {
			return 0, fmt.Errorf("timeout must be greater than zero")
		}
		return time.Duration(value.ValueInt64()) * time.Second, nil
	}

	raw := strings.TrimSpace(os.Getenv("ASC_TIMEOUT"))
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("ASC_TIMEOUT must be a positive integer")
	}
	return time.Duration(seconds) * time.Second, nil
}

// Resources registers the supported infra-gateway and UAM resources. See docs/legacy-
// registrations.md for omitted legacy capabilities.
func (p *ASCProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		vpc.NewResource,
		subnet.NewResource,
		virtualmachine.NewResource,
		vmgroup.NewResource,
		volume.NewResource,
		volumeattachment.NewResource,
		role.NewResource,
		group.NewResource,
	}
}

// DataSources registers lookups supported by infra-gateway and UAM.
func (p *ASCProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		skudatasource.NewDataSource,
		vpcdatasource.NewDataSource,
		subnetdatasource.NewDataSource,
		virtualmachinedatasource.NewDataSource,
		vmgroupdatasource.NewDataSource,
		volumedatasource.NewDataSource,
		roledatasource.NewDataSource,
		groupdatasource.NewDataSource,
	}
}
