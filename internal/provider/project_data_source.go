package provider

import (
	"context"
	"errors"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSourceWithConfigure = &projectDataSource{}
	_ datasource.DataSourceWithConfigure = &projectsDataSource{}
)

type projectModel struct {
	ID                 types.Int64  `tfsdk:"id"`
	Shortname          types.String `tfsdk:"shortname"`
	Description        types.String `tfsdk:"description"`
	ClusterActivated   types.Bool   `tfsdk:"cluster_activated"`
	Disabled           types.Bool   `tfsdk:"disabled"`
	VMsLimit           types.Int64  `tfsdk:"vms_limit"`
	CoresLimit         types.Int64  `tfsdk:"cores_limit"`
	RAMLimit           types.Int64  `tfsdk:"ram_limit"`
	VolumeCounterLimit types.Int64  `tfsdk:"volume_counter_limit"`
	VolumeStorageLimit types.Int64  `tfsdk:"volume_storage_limit"`
}

func newProjectModel(p client.Project) projectModel {
	return projectModel{
		ID:                 types.Int64Value(p.ID),
		Shortname:          types.StringValue(p.Shortname),
		Description:        types.StringValue(p.Description),
		ClusterActivated:   types.BoolValue(p.ClusterActivated),
		Disabled:           types.BoolValue(p.Disabled),
		VMsLimit:           types.Int64Value(p.VMsLimit),
		CoresLimit:         types.Int64Value(p.CoresLimit),
		RAMLimit:           types.Int64Value(p.RAMLimit),
		VolumeCounterLimit: types.Int64Value(p.VolumeCounterLimit),
		VolumeStorageLimit: types.Int64Value(p.VolumeStorageLimit),
	}
}

func projectAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"shortname":            schema.StringAttribute{Computed: true, MarkdownDescription: "Short name of the project."},
		"description":          schema.StringAttribute{Computed: true, MarkdownDescription: "Project description."},
		"cluster_activated":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether clusters are activated for the project."},
		"disabled":             schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the project is disabled."},
		"vms_limit":            schema.Int64Attribute{Computed: true, MarkdownDescription: "Maximum number of VMs."},
		"cores_limit":          schema.Int64Attribute{Computed: true, MarkdownDescription: "Maximum number of CPU cores."},
		"ram_limit":            schema.Int64Attribute{Computed: true, MarkdownDescription: "Maximum amount of RAM (GB)."},
		"volume_counter_limit": schema.Int64Attribute{Computed: true, MarkdownDescription: "Maximum number of volumes."},
		"volume_storage_limit": schema.Int64Attribute{Computed: true, MarkdownDescription: "Maximum total volume storage (GB)."},
	}
}

// ---- simplevm_project ----

type projectDataSource struct{ pd *ProviderData }

// NewProjectDataSource returns the simplevm_project data source.
func NewProjectDataSource() datasource.DataSource { return &projectDataSource{} }

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := projectAttributes()
	attrs["id"] = schema.Int64Attribute{
		Optional: true, Computed: true,
		MarkdownDescription: "Project ID. When omitted, the project the API key is scoped to is returned.",
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a SimpleVM project, by default the one the API key belongs to.",
		Attributes:          attrs,
	}
}

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg projectModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		p   *client.Project
		err error
	)
	if cfg.ID.IsNull() {
		p, err = d.pd.Client.GetProjectByAPIKey(ctx)
	} else {
		p, err = d.pd.Client.GetProject(ctx, cfg.ID.ValueInt64())
		if errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Project not found", "No project with the given id is visible to this API key.")
			return
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	m := newProjectModel(*p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---- simplevm_projects ----

type projectsDataSource struct{ pd *ProviderData }

// NewProjectsDataSource returns the simplevm_projects data source.
func NewProjectsDataSource() datasource.DataSource { return &projectsDataSource{} }

type projectsModel struct {
	ID       types.String   `tfsdk:"id"`
	Projects []projectModel `tfsdk:"projects"`
}

func (d *projectsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_projects"
}

func (d *projectsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	nested := projectAttributes()
	nested["id"] = schema.Int64Attribute{Computed: true, MarkdownDescription: "Project ID."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all SimpleVM projects visible to the API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Placeholder identifier of this data source."},
			"projects": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The projects.",
				NestedObject:        schema.NestedAttributeObject{Attributes: nested},
			},
		},
	}
}

func (d *projectsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *projectsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := d.pd.Client.ListProjects(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list projects", err.Error())
		return
	}
	m := projectsModel{ID: types.StringValue("projects"), Projects: []projectModel{}}
	for _, p := range list {
		m.Projects = append(m.Projects, newProjectModel(p))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
