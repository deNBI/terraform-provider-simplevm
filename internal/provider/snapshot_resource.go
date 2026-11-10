package provider

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithConfigure = &snapshotResource{}

// Snapshots only have a create timeout; they cannot be deleted via the API.
var snapshotTimeouts = opTimeouts{Create: 10 * time.Minute}

const snapshotStatusActive = "active"

// snapshotFailedStatuses are image statuses a snapshot never recovers from.
var snapshotFailedStatuses = []string{"error", "killed", "deleted"}

type snapshotModel struct {
	ID          types.String   `tfsdk:"id"`
	VMID        types.String   `tfsdk:"vm_id"`
	Name        types.String   `tfsdk:"name"`
	Description types.String   `tfsdk:"description"`
	ProjectID   types.Int64    `tfsdk:"project_id"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
	Status      types.String   `tfsdk:"status"`
	CreatedAt   types.String   `tfsdk:"created_at"`
}

type snapshotResource struct{ pd *ProviderData }

// NewSnapshotResource returns the simplevm_snapshot resource.
func NewSnapshotResource() resource.Resource { return &snapshotResource{} }

func (r *snapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_snapshot"
}

func (r *snapshotResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A snapshot image of a SimpleVM VM.\n\n" +
			"Creating a snapshot is asynchronous: the provider polls the project's snapshot images until the " +
			"new image is `active`; see `timeouts`. A snapshot that enters an error status fails the apply.\n\n" +
			"**The SimpleVM API cannot delete snapshots.** Destroying this resource only removes the snapshot " +
			"from state and emits a warning; delete the image in the SimpleVM portal under Images/Snapshots. " +
			"If the image disappears outside Terraform, the resource is removed from state on the next refresh.\n\n" +
			"Every argument replaces the snapshot when changed. **Import is not supported.**",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "OpenStack ID of the snapshot image.",
				PlanModifiers:       keep,
			},
			"vm_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "UUID of the VM to snapshot. Changing it creates a new snapshot.",
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the snapshot image. Changing it creates a new snapshot.",
				PlanModifiers:       replace,
			},
			"description": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Description of the snapshot. Changing it creates a new snapshot.",
				PlanModifiers:       replace,
			},
			"project_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "ID of the SimpleVM project of the snapshot. Defaults to the provider's `project_id`, " +
					"then to the project of the VM. Changing it creates a new snapshot.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()},
			},
			"timeouts": timeoutsAttribute(ctx, snapshotTimeouts),
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Status of the snapshot image as reported by SimpleVM, for example `active`.",
				PlanModifiers:       keep,
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation time of the snapshot as reported by SimpleVM.",
				PlanModifiers:       keep,
			},
		},
	}
}

func (r *snapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (r *snapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan snapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, snapshotTimeouts.Create)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	vmID := plan.VMID.ValueString()
	projectID, d := resolveProjectID(plan.ProjectID, r.pd)
	if d.HasError() {
		// No explicit or provider project: use the VM's.
		vm, err := r.pd.Client.GetVM(ctx, vmID)
		if err != nil {
			resp.Diagnostics.AddError("Unable to read VM "+vmID, err.Error())
			return
		}
		if vm.Project == nil {
			resp.Diagnostics.Append(d...)
			return
		}
		projectID = *vm.Project
	}

	created, err := r.pd.Client.CreateSnapshot(ctx, client.CreateSnapshotRequest{
		Name: plan.Name.ValueString(), Description: plan.Description.ValueString(), VMUUID: vmID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create snapshot", err.Error())
		return
	}

	// Save right away: the API cannot delete snapshots, so losing track of
	// the id would make the next apply create a duplicate.
	plan.ProjectID = types.Int64Value(projectID)
	plan.ID = types.StringValue(created.OpenstackID)
	if created.OpenstackID == "" {
		plan.ID = types.StringUnknown()
	}
	plan.Status, plan.CreatedAt = types.StringNull(), types.StringNull()
	if created.OpenstackID != "" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	img, err := r.waitSnapshot(ctx, projectID, created.OpenstackID, plan.Name.ValueString())
	if img != nil {
		plan.ID = types.StringValue(img.OpenstackID)
		applySnapshotImage(&plan, img)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for snapshot "+plan.Name.ValueString(), err.Error())
	}
}

func (r *snapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imgs, err := r.pd.Client.ListSnapshotImages(ctx, state.ProjectID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Unable to list snapshot images", err.Error())
		return
	}
	i := slices.IndexFunc(imgs, func(im client.Image) bool { return im.OpenstackID == state.ID.ValueString() })
	if i < 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	applySnapshotImage(&state, &imgs[i])
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only runs for timeouts changes; every argument requires replacement.
func (r *snapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan snapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete only forgets the snapshot: the API has no delete endpoint.
func (r *snapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.AddWarning("Snapshot not deleted in SimpleVM",
		fmt.Sprintf("The SimpleVM API cannot delete snapshots. Snapshot %q (%s) was removed from state only; "+
			"delete it in the SimpleVM portal under Images/Snapshots.", state.Name.ValueString(), state.ID.ValueString()))
}

func applySnapshotImage(m *snapshotModel, img *client.Image) {
	m.Status = types.StringValue(img.Status)
	m.CreatedAt = types.StringValue(img.CreatedAt)
}

// waitSnapshot polls the project's snapshot images until the snapshot is
// active. The image is matched on id, or on exact name when the create
// response carried no id. The last observed image is returned (nil if none).
// Transient API errors are retried by client.WaitFor.
func (r *snapshotResource) waitSnapshot(ctx context.Context, projectID int64, id, name string) (*client.Image, error) {
	var last *client.Image
	status := "image not listed yet"
	err := client.WaitFor(ctx, r.pd.PollInterval, func(ctx context.Context) (bool, string, error) {
		imgs, err := r.pd.Client.ListSnapshotImages(ctx, projectID)
		if err != nil {
			if ctx.Err() != nil {
				return false, status, fmt.Errorf("timed out waiting; last observed status %q: %w", status, err)
			}
			return false, status, err
		}
		i := slices.IndexFunc(imgs, func(im client.Image) bool {
			if id != "" {
				return im.OpenstackID == id
			}
			return im.Name == name
		})
		if i < 0 {
			return false, status, nil
		}
		last, status = &imgs[i], "status="+imgs[i].Status
		if slices.Contains(snapshotFailedStatuses, last.Status) {
			return false, status, fmt.Errorf("snapshot image %s entered status %q", last.OpenstackID, last.Status)
		}
		return last.Status == snapshotStatusActive, status, nil
	})
	return last, err
}
