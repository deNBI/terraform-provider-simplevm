package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSourceWithConfigure = &vmsDataSource{}
	_ datasource.DataSourceWithConfigure = &volumesDataSource{}
)

// ---- simplevm_vms ----

type vmItem struct {
	UUID      types.String `tfsdk:"uuid"`
	Name      types.String `tfsdk:"name"`
	State     types.String `tfsdk:"state"`
	TaskState types.String `tfsdk:"task_state"`
	FixedIP   types.String `tfsdk:"fixed_ip"`
	ImageName types.String `tfsdk:"image_name"`
	Flavor    types.String `tfsdk:"flavor_name"`
}

type vmsModel struct {
	ID  types.String `tfsdk:"id"`
	VMs []vmItem     `tfsdk:"vms"`
}

type vmsDataSource struct{ pd *ProviderData }

// NewVMsDataSource returns the simplevm_vms data source.
func NewVMsDataSource() datasource.DataSource { return &vmsDataSource{} }

func (d *vmsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vms"
}

func (d *vmsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the VMs visible to the API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Placeholder identifier of this data source."},
			"vms": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The VMs.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"uuid":        schema.StringAttribute{Computed: true, MarkdownDescription: "VM UUID."},
					"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "VM name."},
					"state":       schema.StringAttribute{Computed: true, MarkdownDescription: "VM state, for example `active` or `stopped`."},
					"task_state":  schema.StringAttribute{Computed: true, MarkdownDescription: "Current task state, empty if none."},
					"fixed_ip":    schema.StringAttribute{Computed: true, MarkdownDescription: "Fixed IP address."},
					"image_name":  schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the image the VM was created from."},
					"flavor_name": schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the VM flavor."},
				}},
			},
		},
	}
}

func (d *vmsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *vmsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := d.pd.Client.ListVMs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list VMs", err.Error())
		return
	}
	m := vmsModel{ID: types.StringValue("vms"), VMs: []vmItem{}}
	for _, v := range list {
		task := ""
		if v.TaskState != nil {
			task = *v.TaskState
		}
		m.VMs = append(m.VMs, vmItem{
			UUID:      types.StringValue(v.UUID),
			Name:      types.StringValue(v.Name),
			State:     types.StringValue(v.VMState),
			TaskState: types.StringValue(task),
			FixedIP:   types.StringValue(v.FixedIP),
			ImageName: types.StringValue(v.ImageName),
			Flavor:    types.StringValue(v.Flavor.Name),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---- simplevm_volumes ----

type volumeItem struct {
	UUID   types.String `tfsdk:"uuid"`
	Name   types.String `tfsdk:"name"`
	Status types.String `tfsdk:"status"`
	Size   types.Int64  `tfsdk:"size"`
	VMName types.String `tfsdk:"vm_name"`
}

type volumesModel struct {
	ID      types.String `tfsdk:"id"`
	Volumes []volumeItem `tfsdk:"volumes"`
}

type volumesDataSource struct{ pd *ProviderData }

// NewVolumesDataSource returns the simplevm_volumes data source.
func NewVolumesDataSource() datasource.DataSource { return &volumesDataSource{} }

func (d *volumesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volumes"
}

func (d *volumesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the volumes visible to the API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Placeholder identifier of this data source."},
			"volumes": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The volumes.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"uuid":    schema.StringAttribute{Computed: true, MarkdownDescription: "Volume UUID."},
					"name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Volume name."},
					"status":  schema.StringAttribute{Computed: true, MarkdownDescription: "Volume status, for example `available` or `in-use`."},
					"size":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Volume size in GB."},
					"vm_name": schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the VM the volume is attached to, empty if detached."},
				}},
			},
		},
	}
}

func (d *volumesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *volumesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := d.pd.Client.ListVolumes(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list volumes", err.Error())
		return
	}
	m := volumesModel{ID: types.StringValue("volumes"), Volumes: []volumeItem{}}
	for _, v := range list {
		vm := ""
		if v.VirtualMachine != nil {
			vm = v.VirtualMachine.Name
		}
		m.Volumes = append(m.Volumes, volumeItem{
			UUID:   types.StringValue(v.UUID),
			Name:   types.StringValue(v.Name),
			Status: types.StringValue(v.Status),
			Size:   types.Int64Value(v.Storage),
			VMName: types.StringValue(vm),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
