package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.ResourceWithConfigure   = &volumeAttachmentResource{}
	_ resource.ResourceWithImportState = &volumeAttachmentResource{}
)

// The attachment has no update; only create and delete are configurable.
var attachmentTimeouts = opTimeouts{Create: 10 * time.Minute, Delete: 10 * time.Minute}

type volumeAttachmentModel struct {
	ID       types.String   `tfsdk:"id"`
	VolumeID types.String   `tfsdk:"volume_id"`
	VMID     types.String   `tfsdk:"vm_id"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
	Device   types.String   `tfsdk:"device"`
	Path     types.String   `tfsdk:"path"`
}

type volumeAttachmentResource struct{ pd *ProviderData }

// NewVolumeAttachmentResource returns the simplevm_volume_attachment resource.
func NewVolumeAttachmentResource() resource.Resource { return &volumeAttachmentResource{} }

func (r *volumeAttachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_attachment"
}

func (r *volumeAttachmentResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Attaches a SimpleVM volume to a VM.\n\n" +
			"Attaching and detaching are asynchronous: the provider polls SimpleVM until the volume is `in-use` " +
			"(attach) or `available` (detach); see `timeouts`. Both arguments replace the attachment when changed.\n\n" +
			"If the volume or the VM is deleted outside Terraform, or the volume is detached outside Terraform, " +
			"the attachment is removed from state on the next refresh. Destroying an attachment whose VM is " +
			"already gone does nothing, since SimpleVM detaches volumes when deleting a VM.\n\n" +
			"The `attach` and `detach` request shapes are assumed from the SimpleVM wiki.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier of the attachment, `<volume_id>/<vm_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"volume_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "UUID of the volume to attach. Changing it replaces the attachment.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"vm_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "UUID of the VM to attach the volume to. Changing it replaces the attachment.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"timeouts": timeoutsAttribute(ctx, attachmentTimeouts),
			"device": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Device name inside the VM, for example `/dev/vdb`.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Mount path of the volume as reported by SimpleVM.",
			},
		},
	}
}

func (r *volumeAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

// isAttachedTo reports whether vol is attached to vm. The API's volume view
// carries no VM uuid, so the VMs are matched on openstack_id when both have
// one and on name otherwise.
func isAttachedTo(vol *client.Volume, vm *client.VM) bool {
	m := vol.VirtualMachine
	if m == nil {
		return false
	}
	if m.OpenstackID != nil && vm.OpenstackID != nil {
		return *m.OpenstackID == *vm.OpenstackID
	}
	return m.Name == vm.Name
}

func (r *volumeAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan volumeAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, attachmentTimeouts.Create)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	volID, vmID := plan.VolumeID.ValueString(), plan.VMID.ValueString()
	vm, err := r.pd.Client.GetVM(ctx, vmID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VM "+vmID, err.Error())
		return
	}
	if _, err := r.pd.Client.VolumeAction(ctx, volID, client.VolumeActionRequest{
		OSAction: client.VolumeActionAttach, VMUUID: vmID,
	}); err != nil {
		resp.Diagnostics.AddError("Unable to attach volume "+volID+" to VM "+vmID, err.Error())
		return
	}

	// Save right away so that a failed wait leaves a tainted attachment
	// Terraform can destroy (detach).
	plan.ID = types.StringValue(volID + "/" + vmID)
	plan.Device, plan.Path = types.StringNull(), types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vol, err := r.waitVolume(ctx, volID, func(v *client.Volume) (bool, error) {
		ok, err := untilVolume(client.VolumeStatusInUse, 0)(v)
		return ok && isAttachedTo(v, vm), err
	})
	if vol != nil && vol.VirtualMachine != nil {
		plan.Device, plan.Path = types.StringValue(vol.Device), types.StringValue(vol.Path)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume "+volID+" to be attached", err.Error())
	}
}

func (r *volumeAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state volumeAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	volID, vmID := state.VolumeID.ValueString(), state.VMID.ValueString()
	vol, err := r.pd.Client.GetVolume(ctx, volID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read volume "+volID, err.Error())
		return
	}
	vm, err := r.pd.Client.GetVM(ctx, vmID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VM "+vmID, err.Error())
		return
	}
	if !isAttachedTo(vol, vm) {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(volID + "/" + vmID)
	state.Device, state.Path = types.StringValue(vol.Device), types.StringValue(vol.Path)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only runs for timeouts changes; both arguments require replacement.
func (r *volumeAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan volumeAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *volumeAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state volumeAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, attachmentTimeouts.Delete)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	volID, vmID := state.VolumeID.ValueString(), state.VMID.ValueString()
	vol, err := r.pd.Client.GetVolume(ctx, volID)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read volume "+volID, err.Error())
		return
	}
	// A destroyed VM has its volumes detached by SimpleVM; a volume that is
	// available or attached elsewhere needs no detach.
	if vol.Status != client.VolumeStatusAvailable {
		vm, err := r.pd.Client.GetVM(ctx, vmID)
		switch {
		case client.IsNotFound(err):
			return
		case err != nil:
			resp.Diagnostics.AddError("Unable to read VM "+vmID, err.Error())
			return
		case !isAttachedTo(vol, vm):
			return
		}
		if _, err := r.pd.Client.VolumeAction(ctx, volID, client.VolumeActionRequest{OSAction: client.VolumeActionDetach}); err != nil {
			resp.Diagnostics.AddError("Unable to detach volume "+volID, err.Error())
			return
		}
	}
	if _, err := r.waitVolume(ctx, volID, func(v *client.Volume) (bool, error) {
		if v == nil {
			return true, nil
		}
		return untilVolume(client.VolumeStatusAvailable, 0)(v)
	}); err != nil {
		resp.Diagnostics.AddError("Error waiting for volume "+volID+" to be detached", err.Error())
	}
}

func (r *volumeAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	volID, vmID, ok := strings.Cut(req.ID, "/")
	if !ok || volID == "" || vmID == "" || strings.Contains(vmID, "/") {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Got %q, expected <volume_id>/<vm_id>.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("volume_id"), volID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vm_id"), vmID)...)
}

// waitVolume reuses the volume resource's polling helper.
func (r *volumeAttachmentResource) waitVolume(ctx context.Context, uuid string, done func(*client.Volume) (bool, error)) (*client.Volume, error) {
	return (&volumeResource{pd: r.pd}).waitVolume(ctx, uuid, done)
}
