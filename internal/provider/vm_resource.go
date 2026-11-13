package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/deNBI/terraform-provider-simplevm/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.ResourceWithConfigure   = &vmResource{}
	_ resource.ResourceWithImportState = &vmResource{}
)

var vmTimeouts = opTimeouts{Create: 30 * time.Minute, Update: 15 * time.Minute, Delete: 15 * time.Minute}

type vmModel struct {
	ID                     types.String   `tfsdk:"id"`
	ProjectID              types.Int64    `tfsdk:"project_id"`
	Name                   types.String   `tfsdk:"name"`
	FullName               types.String   `tfsdk:"full_name"`
	Flavor                 types.String   `tfsdk:"flavor"`
	Image                  types.String   `tfsdk:"image"`
	ResearchEnvironment    types.Object   `tfsdk:"research_environment"`
	PowerState             types.String   `tfsdk:"power_state"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
	OpenstackID            types.String   `tfsdk:"openstack_id"`
	VMState                types.String   `tfsdk:"vm_state"`
	TaskState              types.String   `tfsdk:"task_state"`
	FixedIP                types.String   `tfsdk:"fixed_ip"`
	SSHHost                types.String   `tfsdk:"ssh_host"`
	SSHPort                types.Int64    `tfsdk:"ssh_port"`
	SSHUser                types.String   `tfsdk:"ssh_user"`
	ResearchEnvironmentURL types.String   `tfsdk:"research_environment_url"`
	CreatedAt              types.String   `tfsdk:"created_at"`
}

type researchEnvModel struct {
	Template   types.String `tfsdk:"template"`
	UserPath   types.String `tfsdk:"user_path"`
	CreateOnly types.Bool   `tfsdk:"create_only"`
}

type vmResource struct{ pd *ProviderData }

// NewVMResource returns the simplevm_vm resource.
func NewVMResource() resource.Resource { return &vmResource{} }

func (r *vmResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm"
}

func (r *vmResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A SimpleVM virtual machine, optionally with a research environment.\n\n" +
			"Creating a VM is asynchronous: the provider polls SimpleVM until the VM is `active` and, " +
			"with a `research_environment`, until its playbook has finished. A failed playbook " +
			"(`task_state = playbook_failed`) fails the apply and the VM is tainted, so the next apply " +
			"replaces it. Stopping, resuming and deleting are polled the same way; see `timeouts`. " +
			"If a VM created with `power_state = \"stopped\"` cannot be stopped, the apply only warns: " +
			"the VM is kept and the next plan shows the stop as an in-place update.\n\n" +
			"`power_state` only takes `active` or `stopped` and is the only argument that can change " +
			"in place; every other argument replaces the VM. While the VM is in a transient state " +
			"(for example `building` or `rebooting`) a refresh does not report `power_state` drift.\n\n" +
			"`ssh_host`, `ssh_port` and `ssh_user` tell how to log in over SSH, usually through the " +
			"site's SSH gateway on a port of the VM's own. SimpleVM copies the public SSH key from the " +
			"SimpleVM profile of the VM's owner (the owner of the API key) into `authorized_keys` of the " +
			"new VM, so set a key in your profile before creating VMs. The provider never sees the " +
			"private key: pass it to `ssh` or to a `connection` block yourself.\n\n" +
			"Importing does not restore `research_environment`, because SimpleVM does not report it. " +
			"Add `lifecycle { ignore_changes = [research_environment] }` to an imported research " +
			"environment VM to keep it from being replaced.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "UUID of the VM in SimpleVM.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "ID of the SimpleVM project the VM belongs to. Defaults to the provider's `project_id`. " +
					"Changing it replaces the VM.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the VM, 1 to 100 characters. SimpleVM appends a suffix to it; see `full_name`. " +
					"Changing it replaces the VM, unless `full_name` is the new name plus such a suffix (for example after an import).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					nameRequiresReplace,
					"Replaces the VM unless full_name is the new name plus the suffix SimpleVM appends.",
					"Replaces the VM unless `full_name` is the new name plus the suffix SimpleVM appends.",
				)},
				Validators: []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"full_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the VM in SimpleVM: `name` plus the suffix SimpleVM appends, for example `my-vm-faaef`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"flavor": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the flavor, for example `de.NBI default`; see the `simplevm_flavors` data source. Changing it replaces the VM.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"image": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the base image or snapshot image; see the `simplevm_images` and `simplevm_snapshot_images` data sources. Changing it replaces the VM.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"research_environment": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Research environment to install on the VM. The image must support the template " +
					"(see `resenv_template_name` of the `simplevm_image` data source). Changing it replaces the VM.",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"template": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Name of the research environment template, for example `rstudio`.",
					},
					"user_path": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(""),
						MarkdownDescription: "URL path under which the research environment is served. Defaults to `\"\"`, which lets SimpleVM choose.",
					},
					"create_only": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
						MarkdownDescription: "Install the research environment without making it reachable through the SimpleVM portal. Defaults to `false`.",
					},
				},
			},
			"power_state": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(client.VMStateActive),
				MarkdownDescription: "Desired power state: `active` (default) or `stopped`. Changing it stops or resumes the VM in place. " +
					"Only `active` and `stopped` VM states are read back, so transient states do not cause drift.",
				Validators: []validator.String{stringvalidator.OneOf(client.VMStateActive, client.VMStateStopped)},
			},
			"timeouts": timeoutsAttribute(ctx, vmTimeouts),
			"openstack_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "OpenStack ID of the VM.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"vm_state": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current VM state as reported by SimpleVM, for example `active`, `stopped` or `building`.",
			},
			"task_state": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current task state as reported by SimpleVM, for example `building_playbook`; empty when idle.",
			},
			"fixed_ip": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Fixed (internal) IP address of the VM.",
			},
			"ssh_host": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Host to connect to over SSH: usually the SSH gateway of the site, not the VM's `fixed_ip`. " +
					"Null while SimpleVM reports no SSH access for the VM.",
			},
			"ssh_port": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Port to connect to over SSH; on the gateway each VM has a port of its own. Null like `ssh_host`.",
			},
			"ssh_user": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "User to log in as over SSH, for example `ubuntu`. Null like `ssh_host`, or when SimpleVM names no user.",
			},
			"research_environment_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "URL of the research environment; empty for VMs without one.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation time of the VM as reported by SimpleVM.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *vmResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd, _ = providerDataInto(req.ProviderData, &resp.Diagnostics)
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, vmTimeouts.Create)
	resp.Diagnostics.Append(d...)
	projectID, d := resolveProjectID(plan.ProjectID, r.pd)
	resp.Diagnostics.Append(d...)
	resenv, d := researchEnvironment(ctx, plan.ResearchEnvironment)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	created, err := r.pd.Client.CreateVM(ctx, client.CreateVMRequest{
		ProjectID:           projectID,
		ImageName:           plan.Image.ValueString(),
		FlavorName:          plan.Flavor.ValueString(),
		VMName:              plan.Name.ValueString(),
		ResearchEnvironment: resenv,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create VM", err.Error())
		return
	}
	if created.UUID == "" {
		resp.Diagnostics.AddError("Unable to create VM",
			fmt.Sprintf("SimpleVM API returned no VM uuid. Check the SimpleVM portal for a VM named %q and import or delete it.", plan.Name.ValueString()))
		return
	}

	// Save the VM right away so that a failed wait leaves a tainted resource
	// Terraform can destroy, instead of an orphaned VM.
	plan.ID = types.StringValue(created.UUID)
	plan.ProjectID = types.Int64Value(projectID)
	for _, s := range []*types.String{&plan.FullName, &plan.OpenstackID, &plan.VMState, &plan.TaskState, &plan.FixedIP, &plan.SSHHost, &plan.SSHUser, &plan.ResearchEnvironmentURL, &plan.CreatedAt} {
		*s = types.StringNull()
	}
	plan.SSHPort = types.Int64Null()
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vm, err := r.waitVM(ctx, created.UUID, untilCreated(resenv != nil))
	if err != nil {
		if vm != nil {
			applyVM(&plan, vm)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		}
		resp.Diagnostics.AddError("Error waiting for VM "+created.UUID, err.Error())
		return
	}
	power := plan.PowerState
	if power.ValueString() == client.VMStateStopped {
		// The VM itself is fine: a failed stop must not taint it. The next
		// refresh reads power_state from vm_state, so the plan shows a stop.
		stopped, err := r.setPower(ctx, vm, client.VMStateStopped)
		if stopped != nil {
			vm = stopped
		}
		if err != nil {
			resp.Diagnostics.AddWarning("VM created but not stopped",
				fmt.Sprintf("VM %s was created but could not be stopped: %s. The next plan will show an in-place stop.", created.UUID, err))
		}
	}
	applyVM(&plan, vm)
	plan.PowerState = power // the planned value; refresh reports the real one
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vm, err := r.pd.Client.GetVM(ctx, state.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && slices.Contains(client.VMGoneStates, vm.VMState)) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VM "+state.ID.ValueString(), err.Error())
		return
	}
	applyVM(&state, vm)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Update(ctx, vmTimeouts.Update)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	uuid := plan.ID.ValueString()
	vm, err := r.pd.Client.GetVM(ctx, uuid)
	if err != nil {
		// resp.State starts out as the prior state, which is kept.
		resp.Diagnostics.AddError("Unable to read VM "+uuid, err.Error())
		return
	}
	// Everything but power_state and timeouts replaces the VM, so with an
	// unchanged power_state only timeouts changed: leave the VM alone, even
	// when it is in a transient state.
	if plan.PowerState.ValueString() != state.PowerState.ValueString() {
		vm, err = r.setPower(ctx, vm, plan.PowerState.ValueString())
	}
	if vm != nil {
		power := plan.PowerState
		applyVM(&plan, vm)
		if err == nil {
			plan.PowerState = power // the planned value; refresh reports the real one
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to change power state of VM "+uuid, err.Error())
	}
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, vmTimeouts.Delete)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	uuid := state.ID.ValueString()
	if err := r.pd.Client.DeleteVM(ctx, uuid); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete VM "+uuid, err.Error())
		return
	}
	if _, err := r.waitVM(ctx, uuid, untilGone); err != nil {
		resp.Diagnostics.AddError("Error waiting for VM "+uuid+" to be deleted", err.Error())
	}
}

func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// setPower stops or resumes vm so that its vm_state becomes target and waits
// for it. A VM already in the target state is left alone.
func (r *vmResource) setPower(ctx context.Context, vm *client.VM, target string) (*client.VM, error) {
	if vm.VMState == target {
		return vm, nil
	}
	action := client.VMActionResume
	if target == client.VMStateStopped {
		action = client.VMActionStop
	}
	if _, err := r.pd.Client.VMAction(ctx, vm.UUID, action); err != nil {
		return vm, fmt.Errorf("%s: %w", action, err)
	}
	return r.waitVM(ctx, vm.UUID, untilPower(target))
}

// waitVM polls the VM until done reports true or returns an error. done gets
// nil when the VM no longer exists. The last observed VM is returned (nil if
// none was seen); errors name its vm_state and task_state. Transient API
// errors are retried by client.WaitFor.
func (r *vmResource) waitVM(ctx context.Context, uuid string, done func(*client.VM) (bool, error)) (*client.VM, error) {
	var last *client.VM
	status := "none observed yet"
	err := client.WaitFor(ctx, r.pd.PollInterval, func(ctx context.Context) (bool, string, error) {
		vm, err := r.pd.Client.GetVM(ctx, uuid)
		switch {
		case client.IsNotFound(err):
			vm = nil
		case err != nil && ctx.Err() != nil:
			return false, status, fmt.Errorf("timed out waiting; last observed status %q: %w", status, err)
		case err != nil:
			return false, status, err
		default:
			last, status = vm, vmStatus(vm)
		}
		ok, err := done(vm)
		if err != nil {
			return false, status, fmt.Errorf("VM %s: %w", uuid, err)
		}
		return ok, status, nil
	})
	return last, err
}

func vmStatus(vm *client.VM) string {
	return fmt.Sprintf("vm_state=%s, task_state=%s", vm.VMState, taskState(vm))
}

func taskState(vm *client.VM) string {
	if vm.TaskState == nil {
		return ""
	}
	return *vm.TaskState
}

// vmBroken returns an error when vm is gone or in a failure state.
func vmBroken(vm *client.VM) error {
	switch {
	case vm == nil:
		return fmt.Errorf("VM no longer exists")
	case slices.Contains(client.VMFailureStates, vm.VMState):
		return fmt.Errorf("VM entered failure state (%s)", vmStatus(vm))
	case slices.Contains(client.VMGoneStates, vm.VMState):
		return fmt.Errorf("VM is being deleted (%s)", vmStatus(vm))
	}
	return nil
}

// untilCreated is done once the VM is active and, with a research
// environment, its playbook has finished.
func untilCreated(resenv bool) func(*client.VM) (bool, error) {
	return func(vm *client.VM) (bool, error) {
		if err := vmBroken(vm); err != nil {
			return false, err
		}
		ts := taskState(vm)
		if ts == client.TaskStatePlaybookFailed {
			return false, fmt.Errorf("research environment playbook failed (%s)", vmStatus(vm))
		}
		if vm.VMState != client.VMStateActive {
			return false, nil
		}
		if !resenv {
			return true, nil
		}
		return !slices.Contains(client.PlaybookRunningTaskStates, ts) &&
			(vm.Playbook.AllDone() || ts == client.TaskStatePlaybookSuccessful), nil
	}
}

// untilPower is done once the VM has reached the target state and is not
// busy with a transition (see client.VMBusyTaskStates). Idle task states
// such as playbook_successful do not block.
func untilPower(target string) func(*client.VM) (bool, error) {
	return func(vm *client.VM) (bool, error) {
		if err := vmBroken(vm); err != nil {
			return false, err
		}
		return vm.VMState == target && !slices.Contains(client.VMBusyTaskStates, taskState(vm)), nil
	}
}

func untilGone(vm *client.VM) (bool, error) {
	return vm == nil || slices.Contains(client.VMGoneStates, vm.VMState), nil
}

// nameMatches reports whether actual, the name SimpleVM reports, is name
// itself or name plus the suffix SimpleVM appends on create ("-" and a short
// token without dashes, for example "my-vm-faaef").
func nameMatches(name, actual string) bool {
	if actual == name {
		return true
	}
	suffix, ok := strings.CutPrefix(actual, name+"-")
	return ok && suffix != "" && len(suffix) <= 16 && !strings.Contains(suffix, "-")
}

// nameRequiresReplace keeps a name change in place when the VM's full_name
// already matches the new name, as after an import, which sets name to the
// full name.
func nameRequiresReplace(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	var full types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("full_name"), &full)...)
	if full.IsNull() || full.IsUnknown() || req.PlanValue.IsUnknown() {
		resp.RequiresReplace = true
		return
	}
	resp.RequiresReplace = !nameMatches(req.PlanValue.ValueString(), full.ValueString())
}

// applyVM copies the API view of vm into m. name is kept while the reported
// name matches it (see nameMatches); power_state is only taken from stable VM
// states, and flavor, image and project_id only when unset (import), so
// transient states and API naming quirks cause no drift.
func applyVM(m *vmModel, vm *client.VM) {
	m.ID = types.StringValue(vm.UUID)
	m.FullName = types.StringValue(vm.Name)
	if m.Name.IsNull() || m.Name.IsUnknown() || !nameMatches(m.Name.ValueString(), vm.Name) {
		m.Name = types.StringValue(vm.Name)
	}
	m.OpenstackID = types.StringPointerValue(vm.OpenstackID)
	m.VMState = types.StringValue(vm.VMState)
	m.TaskState = types.StringValue(taskState(vm))
	m.FixedIP = types.StringValue(vm.FixedIP)
	m.SSHHost, m.SSHPort, m.SSHUser = sshValues(vm)
	m.ResearchEnvironmentURL = types.StringValue(vm.ResearchEnvironmentURL)
	m.CreatedAt = types.StringPointerValue(vm.CreatedAtDate)
	if vm.VMState == client.VMStateActive || vm.VMState == client.VMStateStopped {
		m.PowerState = types.StringValue(vm.VMState)
	}
	if m.Flavor.IsNull() || m.Flavor.IsUnknown() {
		m.Flavor = types.StringValue(vm.Flavor.Name)
	}
	if m.Image.IsNull() || m.Image.IsUnknown() {
		m.Image = types.StringValue(vm.ImageName)
	}
	if (m.ProjectID.IsNull() || m.ProjectID.IsUnknown()) && vm.Project != nil {
		m.ProjectID = types.Int64Value(*vm.Project)
	}
}

// researchEnvironment converts the research_environment attribute into the
// API request part; nil when it is not set.
func researchEnvironment(ctx context.Context, o types.Object) (*client.ResearchEnvironmentBackend, diag.Diagnostics) {
	if o.IsNull() || o.IsUnknown() {
		return nil, nil
	}
	var re researchEnvModel
	diags := o.As(ctx, &re, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	return &client.ResearchEnvironmentBackend{
		Template:   re.Template.ValueString(),
		UserPath:   re.UserPath.ValueString(),
		CreateOnly: re.CreateOnly.ValueBool(),
	}, diags
}

// sshValues returns ssh_host, ssh_port and ssh_user for vm; all null when
// SimpleVM reports no valid SSH access (see client.VM.SSH).
func sshValues(vm *client.VM) (host types.String, port types.Int64, user types.String) {
	a := vm.SSH()
	if a == nil {
		return types.StringNull(), types.Int64Null(), types.StringNull()
	}
	user = types.StringNull()
	if a.User != "" {
		user = types.StringValue(a.User)
	}
	return types.StringValue(a.Host), types.Int64Value(a.Port), user
}
