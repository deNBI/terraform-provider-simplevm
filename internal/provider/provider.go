// Package provider implements the Terraform/OpenTofu provider for SimpleVM.
package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// DefaultEndpoint is the SimpleVM portal API used when no endpoint is configured.
const DefaultEndpoint = client.DefaultEndpoint

var _ fwprovider.Provider = &simpleVMProvider{}

type simpleVMProvider struct {
	version string
}

type providerModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	APIKey    types.String `tfsdk:"api_key"`
	ProjectID types.Int64  `tfsdk:"project_id"`
}

// resolvedConfig holds provider settings after applying environment fallbacks.
type resolvedConfig struct {
	Endpoint  string
	APIKey    string
	ProjectID int64
	// HasProjectID is false when no default project was configured.
	HasProjectID bool
}

// ProviderData is shared with resources and data sources via Configure.
type ProviderData struct {
	Client *client.Client
	// DefaultProjectID is the provider-level project; 0 means unset.
	DefaultProjectID int64
	PollInterval     time.Duration
}

// DefaultPollInterval is how often long-running operations are polled.
const DefaultPollInterval = 10 * time.Second

// New returns a factory for the SimpleVM provider.
func New(version string) func() fwprovider.Provider {
	return func() fwprovider.Provider {
		return &simpleVMProvider{version: version}
	}
}

func (p *simpleVMProvider) Metadata(_ context.Context, _ fwprovider.MetadataRequest, resp *fwprovider.MetadataResponse) {
	resp.TypeName = "simplevm"
	resp.Version = p.version
}

func (p *simpleVMProvider) Schema(_ context.Context, _ fwprovider.SchemaRequest, resp *fwprovider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The SimpleVM provider manages virtual machines, volumes and snapshots in the de.NBI Cloud via the SimpleVM REST API.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Base URL of the SimpleVM portal API. Defaults to `" + DefaultEndpoint + "`. May also be set with the `SIMPLEVM_ENDPOINT` environment variable.",
			},
			"api_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "SimpleVM API key, sent as the `X-API-KEY` header. May also be set with the `SIMPLEVM_API_KEY` environment variable.",
			},
			"project_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Default SimpleVM project ID used by resources that do not set their own `project_id`. May also be set with the `SIMPLEVM_PROJECT_ID` environment variable.",
			},
		},
	}
}

func (p *simpleVMProvider) Configure(ctx context.Context, req fwprovider.ConfigureRequest, resp *fwprovider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resolved, diags := resolveConfig(cfg, os.Getenv)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	pollInterval, err := parsePollInterval(os.Getenv("SIMPLEVM_POLL_INTERVAL"))
	if err != nil {
		resp.Diagnostics.AddError("Invalid SIMPLEVM_POLL_INTERVAL", err.Error())
		return
	}

	c, err := client.New(client.Config{
		Endpoint:  resolved.Endpoint,
		APIKey:    resolved.APIKey,
		UserAgent: "terraform-provider-simplevm/" + p.version,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create SimpleVM client", err.Error())
		return
	}

	data := &ProviderData{Client: c, PollInterval: pollInterval}
	if resolved.HasProjectID {
		data.DefaultProjectID = resolved.ProjectID
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

// parsePollInterval parses SIMPLEVM_POLL_INTERVAL; empty selects the default.
func parsePollInterval(v string) (time.Duration, error) {
	if v == "" {
		return DefaultPollInterval, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("SIMPLEVM_POLL_INTERVAL must be a positive Go duration such as \"10s\", got %q", v)
	}
	return d, nil
}

// resolveConfig merges configuration values with environment variables
// (configuration wins) and validates the result.
func resolveConfig(cfg providerModel, getenv func(string) string) (resolvedConfig, diag.Diagnostics) {
	var diags diag.Diagnostics

	for _, u := range []struct {
		name    string
		unknown bool
	}{
		{"endpoint", cfg.Endpoint.IsUnknown()},
		{"api_key", cfg.APIKey.IsUnknown()},
		{"project_id", cfg.ProjectID.IsUnknown()},
	} {
		if u.unknown {
			diags.AddAttributeError(path.Root(u.name),
				"Unknown SimpleVM provider value",
				fmt.Sprintf("The provider cannot be configured while %q is unknown. Set it to a known value, or use the corresponding SIMPLEVM_* environment variable.", u.name))
		}
	}
	if diags.HasError() {
		return resolvedConfig{}, diags
	}

	out := resolvedConfig{Endpoint: DefaultEndpoint}

	if v := getenv("SIMPLEVM_ENDPOINT"); v != "" {
		out.Endpoint = v
	}
	if !cfg.Endpoint.IsNull() && cfg.Endpoint.ValueString() != "" {
		out.Endpoint = cfg.Endpoint.ValueString()
	}

	out.APIKey = getenv("SIMPLEVM_API_KEY")
	if !cfg.APIKey.IsNull() && cfg.APIKey.ValueString() != "" {
		out.APIKey = cfg.APIKey.ValueString()
	}
	if out.APIKey == "" {
		diags.AddAttributeError(path.Root("api_key"),
			"Missing SimpleVM API key",
			"api_key must be set or SIMPLEVM_API_KEY exported")
	}

	switch {
	case !cfg.ProjectID.IsNull():
		out.ProjectID, out.HasProjectID = cfg.ProjectID.ValueInt64(), true
	case getenv("SIMPLEVM_PROJECT_ID") != "":
		id, err := strconv.ParseInt(getenv("SIMPLEVM_PROJECT_ID"), 10, 64)
		if err != nil {
			diags.AddAttributeError(path.Root("project_id"),
				"Invalid SIMPLEVM_PROJECT_ID",
				fmt.Sprintf("SIMPLEVM_PROJECT_ID must be an integer: %s", err))
		} else {
			out.ProjectID, out.HasProjectID = id, true
		}
	}

	return out, diags
}

func (p *simpleVMProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewVMResource,
	}
}

func (p *simpleVMProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewProjectDataSource, NewProjectsDataSource,
		NewFlavorDataSource, NewFlavorsDataSource,
		NewImageDataSource, NewImagesDataSource, NewSnapshotImagesDataSource,
		NewVMsDataSource, NewVolumesDataSource,
	}
}
