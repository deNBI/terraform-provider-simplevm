package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSourceWithConfigure = &imageDataSource{}
	_ datasource.DataSourceWithConfigure = &imagesDataSource{}
	_ datasource.DataSourceWithConfigure = &snapshotImagesDataSource{}
)

type imageFields struct {
	OpenstackID        types.String `tfsdk:"openstack_id"`
	Name               types.String `tfsdk:"name"`
	Title              types.String `tfsdk:"title"`
	OSDistro           types.String `tfsdk:"os_distro"`
	OSVersion          types.String `tfsdk:"os_version"`
	Tags               []string     `tfsdk:"tags"`
	MinRAM             types.Int64  `tfsdk:"min_ram"`
	MinDisk            types.Int64  `tfsdk:"min_disk"`
	IsSnapshot         types.Bool   `tfsdk:"is_snapshot"`
	ResenvTemplateName types.String `tfsdk:"resenv_template_name"`
}

func newImageFields(i client.Image) imageFields {
	tags := i.Tags
	if tags == nil {
		tags = []string{}
	}
	return imageFields{
		OpenstackID:        types.StringValue(i.OpenstackID),
		Name:               types.StringValue(i.Name),
		Title:              types.StringValue(i.Title),
		OSDistro:           types.StringValue(i.OSDistro),
		OSVersion:          types.StringValue(i.OSVersion),
		Tags:               tags,
		MinRAM:             types.Int64Value(i.MinRAM),
		MinDisk:            types.Int64Value(i.MinDisk),
		IsSnapshot:         types.BoolValue(i.IsSnapshot),
		ResenvTemplateName: types.StringValue(i.ResenvTemplateName),
	}
}

func imageComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"openstack_id":         schema.StringAttribute{Computed: true, MarkdownDescription: "OpenStack ID of the image."},
		"title":                schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable image title."},
		"os_distro":            schema.StringAttribute{Computed: true, MarkdownDescription: "Operating system distribution."},
		"os_version":           schema.StringAttribute{Computed: true, MarkdownDescription: "Operating system version."},
		"tags":                 schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Image tags."},
		"min_ram":              schema.Int64Attribute{Computed: true, MarkdownDescription: "Minimum RAM in MB."},
		"min_disk":             schema.Int64Attribute{Computed: true, MarkdownDescription: "Minimum disk size in GB."},
		"is_snapshot":          schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the image is a snapshot image."},
		"resenv_template_name": schema.StringAttribute{Computed: true, MarkdownDescription: "Research environment template of the image, empty if none."},
	}
}

func imageListAttribute(desc string) schema.ListNestedAttribute {
	nested := imageComputedAttributes()
	nested["name"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Image name."}
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: desc,
		NestedObject:        schema.NestedAttributeObject{Attributes: nested},
	}
}

// ---- simplevm_image ----

type imageDataSource struct{ pd *ProviderData }

// NewImageDataSource returns the simplevm_image data source.
func NewImageDataSource() datasource.DataSource { return &imageDataSource{} }

type imageModel struct {
	ID               types.String `tfsdk:"id"`
	ProjectID        types.Int64  `tfsdk:"project_id"`
	IncludeSnapshots types.Bool   `tfsdk:"include_snapshots"`
	imageFields
}

func (d *imageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_image"
}

func (d *imageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := imageComputedAttributes()
	attrs["id"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Identifier, the OpenStack ID of the image."}
	attrs["project_id"] = projectIDAttribute
	attrs["name"] = schema.StringAttribute{Required: true, MarkdownDescription: "Exact image name, for example `Ubuntu-22.04`."}
	attrs["include_snapshots"] = schema.BoolAttribute{
		Optional:            true,
		MarkdownDescription: "Also search snapshot images. Regular images win on a name clash. Defaults to `false`.",
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a VM image by name, including its research environment template (if any).",
		Attributes:          attrs,
	}
}

func (d *imageDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *imageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg imageModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pid, diags := resolveProjectID(cfg.ProjectID, d.pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	images, err := d.pd.Client.ListImages(ctx, pid)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list images", err.Error())
		return
	}
	if cfg.IncludeSnapshots.ValueBool() {
		snaps, err := d.pd.Client.ListSnapshotImages(ctx, pid)
		if err != nil {
			resp.Diagnostics.AddError("Unable to list snapshot images", err.Error())
			return
		}
		images = append(images, snaps...)
	}
	want := cfg.Name.ValueString()
	var names []string
	for _, img := range images {
		names = append(names, img.Name)
	}
	idx := slices.IndexFunc(images, func(i client.Image) bool { return i.Name == want })
	if idx < 0 {
		resp.Diagnostics.AddAttributeError(pathRoot("name"), "Image not found",
			fmt.Sprintf("No image named %q in project %d. Available images: %s", want, pid, sortedJoin(names)))
		return
	}
	img := images[idx]
	m := imageModel{
		ID:               types.StringValue(img.OpenstackID),
		ProjectID:        types.Int64Value(pid),
		IncludeSnapshots: cfg.IncludeSnapshots,
		imageFields:      newImageFields(img),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---- simplevm_images / simplevm_snapshot_images ----

type imagesModel struct {
	ID        types.String  `tfsdk:"id"`
	ProjectID types.Int64   `tfsdk:"project_id"`
	Tag       types.String  `tfsdk:"tag"`
	Images    []imageFields `tfsdk:"images"`
}

type imagesDataSource struct{ pd *ProviderData }

// NewImagesDataSource returns the simplevm_images data source.
func NewImagesDataSource() datasource.DataSource { return &imagesDataSource{} }

func (d *imagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_images"
}

func (d *imagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the base images available in a project.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Identifier, the project ID."},
			"project_id": projectIDAttribute,
			"tag":        schema.StringAttribute{Optional: true, MarkdownDescription: "Only return images whose `tags` contain exactly this tag."},
			"images":     imageListAttribute("The images."),
		},
	}
}

func (d *imagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *imagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	readImageList(ctx, req, resp, d.pd, false)
}

type snapshotImagesDataSource struct{ pd *ProviderData }

// NewSnapshotImagesDataSource returns the simplevm_snapshot_images data source.
func NewSnapshotImagesDataSource() datasource.DataSource { return &snapshotImagesDataSource{} }

func (d *snapshotImagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_snapshot_images"
}

func (d *snapshotImagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the snapshot images of a project.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Identifier, the project ID."},
			"project_id": projectIDAttribute,
			"images":     imageListAttribute("The snapshot images."),
		},
	}
}

func (d *snapshotImagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (d *snapshotImagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	readImageList(ctx, req, resp, d.pd, true)
}

func readImageList(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse, pd *ProviderData, snapshots bool) {
	var cfg imagesModel
	// simplevm_snapshot_images has no tag attribute; read only what exists.
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathRoot("project_id"), &cfg.ProjectID)...)
	if !snapshots {
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathRoot("tag"), &cfg.Tag)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	pid, diags := resolveProjectID(cfg.ProjectID, pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		list []client.Image
		err  error
	)
	if snapshots {
		list, err = pd.Client.ListSnapshotImages(ctx, pid)
	} else {
		list, err = pd.Client.ListImages(ctx, pid)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to list images", err.Error())
		return
	}
	out := []imageFields{}
	for _, img := range list {
		if !cfg.Tag.IsNull() && !slices.Contains(img.Tags, cfg.Tag.ValueString()) {
			continue
		}
		out = append(out, newImageFields(img))
	}
	id := types.StringValue(fmt.Sprint(pid))
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("project_id"), types.Int64Value(pid))...)
	if !snapshots {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("tag"), cfg.Tag)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("images"), out)...)
}
