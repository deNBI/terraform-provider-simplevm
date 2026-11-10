package provider

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.ResourceWithConfigure   = &volumeResource{}
	_ resource.ResourceWithImportState = &volumeResource{}
)

var volumeTimeouts = opTimeouts{Create: 10 * time.Minute, Update: 10 * time.Minute, Delete: 10 * time.Minute}

type volumeModel struct {
	ID               types.String   `tfsdk:"id"`
	ProjectID        types.Int64    `tfsdk:"project_id"`
	Name             types.String   `tfsdk:"name"`
	Size             types.Int64    `tfsdk:"size"`
	SourceVolumeID   types.String   `tfsdk:"source_volume_id"`
	VolumeSnapshotID types.String   `tfsdk:"volume_snapshot_id"`
	Timeouts         timeouts.Value `tfsdk:"timeouts"`
	OpenstackID      types.String   `tfsdk:"openstack_id"`
	Status           types.String   `tfsdk:"status"`
	Device           types.String   `tfsdk:"device"`
	Path             types.String   `tfsdk:"path"`
}

// sizeShrinks reports whether the planned size is known and smaller than the
// state size. Unknown or null plans never require replacement.
func sizeShrinks(req planmodifier.Int64Request) bool {
	return !req.PlanValue.IsUnknown() && !req.PlanValue.IsNull() && !req.StateValue.IsNull() &&
		req.PlanValue.ValueInt64() < req.StateValue.ValueInt64()
}

type volumeResource struct{ pd *ProviderData }

// NewVolumeResource returns the simplevm_volume resource.
func NewVolumeResource() resource.Resource { return &volumeResource{} }

func (r *volumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume"
}

func (r *volumeResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A SimpleVM block storage volume.\n\n" +
			"Creating, growing and deleting a volume are asynchronous: the provider polls SimpleVM until the " +
			"volume is `available` (or gone, for delete); see `timeouts`. A volume that enters an error " +
			"status fails the apply.\n\n" +
			"`name` and `size` change in place: a rename is immediate and a larger `size` extends the volume. " +
			"Shrinking a volume is not possible in OpenStack, so a smaller `size` replaces the volume and " +
			"destroys its data. Every other argument replaces the volume. Attach a volume to a VM with " +
			"`simplevm_volume_attachment`.\n\n" +
			"The `extend` and `rename` request shapes are assumed from the SimpleVM wiki.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "UUID of the volume in SimpleVM.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "ID of the SimpleVM project the volume belongs to. Defaults to the provider's `project_id`. " +
					"Changing it replaces the volume.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the volume. Changing it renames the volume in place.",
			},
			"size": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "Size of the volume in GiB. Increasing it extends the volume in place. " +
					"Shrinking a volume replaces it (data loss).",
				Validators: []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = sizeShrinks(req)
					},
					"A smaller size replaces the volume (data loss).",
					"A smaller `size` replaces the volume (data loss).",
				)},
			},
			"source_volume_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "UUID of an existing volume to copy the new volume from. Changing it replaces the volume.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"volume_snapshot_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "ID of a volume snapshot to create the new volume from. Changing it replaces the volume.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"timeouts": timeoutsAttribute(ctx, volumeTimeouts),
			"openstack_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "OpenStack ID of the volume.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current volume status as reported by SimpleVM, for example `available` or `in-use`.",
			},
			"device": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Device name inside the VM the volume is attached to, for example `/dev/vdb`; empty when detached.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Mount path of the volume as reported by SimpleVM; empty when not mounted.",
			},
		},
	}
}

func (r *volumeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (r *volumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan volumeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, volumeTimeouts.Create)
	resp.Diagnostics.Append(d...)
	projectID, d := resolveProjectID(plan.ProjectID, r.pd)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	created, err := r.pd.Client.CreateVolume(ctx, client.CreateVolumeRequest{
		ProjectID:      projectID,
		Storage:        plan.Size.ValueInt64(),
		Name:           plan.Name.ValueString(),
		SourceVolumeID: plan.SourceVolumeID.ValueString(),
		VolumeSnapID:   plan.VolumeSnapshotID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create volume", err.Error())
		return
	}

	// Save the volume right away so that a failed wait leaves a tainted
	// resource Terraform can destroy, instead of an orphaned volume.
	plan.ID = types.StringValue(created.UUID)
	plan.ProjectID = types.Int64Value(projectID)
	plan.OpenstackID = types.StringValue(created.OpenstackID)
	for _, s := range []*types.String{&plan.Status, &plan.Device, &plan.Path} {
		*s = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vol, err := r.waitVolume(ctx, created.UUID, untilVolume(client.VolumeStatusAvailable, 0))
	if vol != nil {
		applyVolume(&plan, vol)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume "+created.UUID, err.Error())
	}
}

func (r *volumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state volumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vol, err := r.pd.Client.GetVolume(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read volume "+state.ID.ValueString(), err.Error())
		return
	}
	applyVolume(&state, vol)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *volumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state volumeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Update(ctx, volumeTimeouts.Update)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	uuid := plan.ID.ValueString()
	vol, err := r.pd.Client.GetVolume(ctx, uuid)
	if err != nil {
		// Keep the prior state rather than the plan, which has unknown values.
		resp.State.Raw = req.State.Raw
		resp.Diagnostics.AddError("Unable to read volume "+uuid, err.Error())
		return
	}
	// Extending leaves the volume in the status it had before.
	target := vol.Status
	extend := plan.Size.ValueInt64() > state.Size.ValueInt64()
	if extend && target != client.VolumeStatusAvailable && target != client.VolumeStatusInUse {
		resp.State.Raw = req.State.Raw
		resp.Diagnostics.AddError("Unable to extend volume "+uuid,
			fmt.Sprintf("volume has status %q; it must be %q or %q to be extended", target, client.VolumeStatusAvailable, client.VolumeStatusInUse))
		return
	}

	// Whatever happens below, state keeps what was not changed yet.
	next := state
	next.Timeouts = plan.Timeouts
	fail := func(summary string, err error) {
		applyVolume(&next, vol)
		resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
		resp.Diagnostics.AddError(summary, err.Error())
	}

	if plan.Name.ValueString() != state.Name.ValueString() {
		renamed, err := r.pd.Client.VolumeAction(ctx, uuid, client.VolumeActionRequest{
			OSAction: "rename", NewName: plan.Name.ValueString(),
		})
		if err != nil {
			fail("Unable to rename volume "+uuid, err)
			return
		}
		vol = renamed
	}
	if extend {
		if _, err := r.pd.Client.VolumeAction(ctx, uuid, client.VolumeActionRequest{
			OSAction: "extend", Size: plan.Size.ValueInt64(),
		}); err != nil {
			fail("Unable to extend volume "+uuid, err)
			return
		}
		extended, err := r.waitVolume(ctx, uuid, untilVolume(target, plan.Size.ValueInt64()))
		if extended != nil {
			vol = extended
		}
		if err != nil {
			fail("Error waiting for volume "+uuid+" to be extended", err)
			return
		}
	}
	applyVolume(&plan, vol)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *volumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state volumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, volumeTimeouts.Delete)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	uuid := state.ID.ValueString()
	if err := r.pd.Client.DeleteVolume(ctx, uuid); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete volume "+uuid, err.Error())
		return
	}
	if _, err := r.waitVolume(ctx, uuid, untilVolumeGone); err != nil {
		resp.Diagnostics.AddError("Error waiting for volume "+uuid+" to be deleted", err.Error())
	}
}

func (r *volumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// waitVolume polls the volume until done reports true or returns an error.
// done gets nil when the volume no longer exists. The last observed volume is
// returned (nil if none was seen); errors name its status.
func (r *volumeResource) waitVolume(ctx context.Context, uuid string, done func(*client.Volume) (bool, error)) (*client.Volume, error) {
	var last *client.Volume
	status := "none observed yet"
	err := client.WaitFor(ctx, r.pd.PollInterval, func(ctx context.Context) (bool, string, error) {
		vol, err := r.pd.Client.GetVolume(ctx, uuid)
		switch {
		case client.IsNotFound(err):
			vol = nil
		case err != nil && ctx.Err() != nil:
			return false, status, fmt.Errorf("timed out waiting; last observed status %q: %w", status, err)
		case err != nil:
			return false, status, err
		default:
			last, status = vol, "status="+vol.Status
		}
		ok, err := done(vol)
		if err != nil {
			return false, status, fmt.Errorf("volume %s: %w", uuid, err)
		}
		return ok, status, nil
	})
	return last, err
}

// untilVolume is done once the volume has the target status and at least
// minSize GiB.
func untilVolume(target string, minSize int64) func(*client.Volume) (bool, error) {
	return func(v *client.Volume) (bool, error) {
		switch {
		case v == nil:
			return false, fmt.Errorf("volume no longer exists")
		case slices.Contains(client.VolumeFailureStates, v.Status):
			return false, fmt.Errorf("volume entered failure state (status=%s)", v.Status)
		}
		return v.Status == target && v.Storage >= minSize, nil
	}
}

func untilVolumeGone(v *client.Volume) (bool, error) { return v == nil, nil }

// applyVolume copies the API view of v into m. project_id is only taken when
// unset (import).
func applyVolume(m *volumeModel, v *client.Volume) {
	m.Name = types.StringValue(v.Name)
	m.Size = types.Int64Value(v.Storage)
	m.OpenstackID = types.StringPointerValue(v.OpenstackID)
	m.Status = types.StringValue(v.Status)
	m.Device = types.StringValue(v.Device)
	m.Path = types.StringValue(v.Path)
	if m.ProjectID.IsNull() || m.ProjectID.IsUnknown() {
		if id, err := v.ProjectID.Int64(); err == nil {
			m.ProjectID = types.Int64Value(id)
		}
	}
}
