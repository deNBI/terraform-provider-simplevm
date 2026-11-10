package provider

import (
	"context"
	"fmt"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSourceWithConfigure = &flavorDataSource{}
	_ datasource.DataSourceWithConfigure = &flavorsDataSource{}
)

type flavorFields struct {
	Name               types.String  `tfsdk:"name"`
	VCPUs              types.Int64   `tfsdk:"vcpus"`
	RAM                types.Int64   `tfsdk:"ram"`
	RootDisk           types.Int64   `tfsdk:"root_disk"`
	EphemeralDisk      types.Int64   `tfsdk:"ephemeral_disk"`
	GPU                types.Int64   `tfsdk:"gpu"`
	CreditsCostPerHour types.Float64 `tfsdk:"credits_cost_per_hour"`
}

func newFlavorFields(f client.Flavor) flavorFields {
	return flavorFields{
		Name:               types.StringValue(f.Name),
		VCPUs:              types.Int64Value(f.VCPUs),
		RAM:                types.Int64Value(f.RAM),
		RootDisk:           types.Int64Value(f.RootDisk),
		EphemeralDisk:      types.Int64Value(f.EphemeralDisk),
		GPU:                types.Int64Value(f.GPU),
		CreditsCostPerHour: types.Float64Value(f.CreditsCostPerHour),
	}
}

// flavorComputedAttributes are the computed attributes shared by both data sources.
func flavorComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"vcpus":                 schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of virtual CPUs."},
		"ram":                   schema.Int64Attribute{Computed: true, MarkdownDescription: "RAM in MB."},
		"root_disk":             schema.Int64Attribute{Computed: true, MarkdownDescription: "Root disk size in GB."},
		"ephemeral_disk":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Ephemeral disk size in GB."},
		"gpu":                   schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of GPUs."},
		"credits_cost_per_hour": schema.Float64Attribute{Computed: true, MarkdownDescription: "Credits consumed per hour."},
	}
}

var projectIDAttribute = schema.Int64Attribute{
	Optional:            true,
	Computed:            true,
	MarkdownDescription: "SimpleVM project ID. Defaults to the provider `project_id`; the effective value is exported.",
}

// ---- simplevm_flavor ----

type flavorDataSource struct{ pd *ProviderData }

// NewFlavorDataSource returns the simplevm_flavor data source.
func NewFlavorDataSource() datasource.DataSource { return &flavorDataSource{} }

type flavorModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.Int64  `tfsdk:"project_id"`
	flavorFields
}

func (d *flavorDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_flavor"
}

func (d *flavorDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := flavorComputedAttributes()
	attrs["id"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Identifier, `<project_id>/<name>`."}
	attrs["project_id"] = projectIDAttribute
	attrs["name"] = schema.StringAttribute{Required: true, MarkdownDescription: "Exact flavor name, for example `de.NBI small`."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a VM flavor (size) by name.",
		Attributes:          attrs,
	}
}

func (d *flavorDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *flavorDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg flavorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pid, diags := resolveProjectID(cfg.ProjectID, d.pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.pd.Client.ListFlavors(ctx, pid)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list flavors", err.Error())
		return
	}
	var names []string
	for _, f := range list {
		names = append(names, f.Name)
		if f.Name == cfg.Name.ValueString() {
			m := flavorModel{
				ID:           types.StringValue(fmt.Sprintf("%d/%s", pid, f.Name)),
				ProjectID:    types.Int64Value(pid),
				flavorFields: newFlavorFields(f),
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.Diagnostics.AddAttributeError(pathRoot("name"), "Flavor not found",
		fmt.Sprintf("No flavor named %q in project %d. Available flavors: %s", cfg.Name.ValueString(), pid, sortedJoin(names)))
}

// ---- simplevm_flavors ----

type flavorsDataSource struct{ pd *ProviderData }

// NewFlavorsDataSource returns the simplevm_flavors data source.
func NewFlavorsDataSource() datasource.DataSource { return &flavorsDataSource{} }

type flavorsModel struct {
	ID        types.String   `tfsdk:"id"`
	ProjectID types.Int64    `tfsdk:"project_id"`
	Flavors   []flavorFields `tfsdk:"flavors"`
}

func (d *flavorsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_flavors"
}

func (d *flavorsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	nested := flavorComputedAttributes()
	nested["name"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Flavor name."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the VM flavors (sizes) available in a project.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Identifier, the project ID."},
			"project_id": projectIDAttribute,
			"flavors": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The flavors.",
				NestedObject:        schema.NestedAttributeObject{Attributes: nested},
			},
		},
	}
}

func (d *flavorsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *flavorsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg flavorsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pid, diags := resolveProjectID(cfg.ProjectID, d.pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.pd.Client.ListFlavors(ctx, pid)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list flavors", err.Error())
		return
	}
	m := flavorsModel{ID: types.StringValue(fmt.Sprint(pid)), ProjectID: types.Int64Value(pid), Flavors: []flavorFields{}}
	for _, f := range list {
		m.Flavors = append(m.Flavors, newFlavorFields(f))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
