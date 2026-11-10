package client

import (
	"bytes"
	"encoding/json"
	"slices"
)

// VM action names accepted by POST vms/{uuid}/action/.
const (
	VMActionStop   = "stop"
	VMActionResume = "resume"
)

// VM states.
const (
	VMStateDeleted  = "deleted"
	VMStateActive   = "active"
	VMStateStopped  = "stopped"
	VMStateBuilding = "building"
)

// Volume statuses.
const (
	VolumeStatusAvailable = "available"
	VolumeStatusInUse     = "in-use"
	VolumeStatusDeleted   = "deleted"
)

// Research environment task states.
const (
	// TaskStatePlaybookFailed is the task state of a VM whose playbook failed.
	TaskStatePlaybookFailed = "playbook_failed"
	// TaskStatePlaybookSuccessful is the task state of a VM whose playbook
	// finished; SimpleVM may keep reporting it while the VM is idle.
	TaskStatePlaybookSuccessful = "playbook_successful"
)

var (
	// VMFailureStates are terminal VM states indicating a failed operation.
	VMFailureStates = []string{"error", "creation_failed"}
	// VMGoneStates are VM states meaning the VM no longer exists for our purposes.
	VMGoneStates = []string{VMStateDeleted, "soft-delete", "marked_as_deletable"}
	// VMBusyTaskStates are (OpenStack) task states of a VM in transition; a
	// power change is not complete while one of them is reported.
	VMBusyTaskStates = []string{
		"scheduling", "spawning", "networking", "block_device_mapping",
		"powering-off", "powering-on",
		"rebooting", "reboot_pending", "reboot_started",
		"rebooting_hard", "reboot_pending_hard", "reboot_started_hard",
		"pausing", "unpausing", "suspending", "resuming",
		"rescuing", "unrescuing", "rebuilding", "shelving", "unshelving", "deleting",
	}
	// PlaybookRunningTaskStates are task states while the research environment is provisioned.
	PlaybookRunningTaskStates = []string{"prepare_playbook_build", "building_playbook", "checking_ssh_connection"}
	// VolumeFailureStates are terminal volume statuses indicating failure.
	VolumeFailureStates = []string{"error", "creation_failed", "error_extending"}
)

// Project is a SimpleVM project the API key has access to.
type Project struct {
	ID                 int64  `json:"id"`
	Shortname          string `json:"shortname"`
	Description        string `json:"description"`
	ClusterActivated   bool   `json:"cluster_activated"`
	Disabled           bool   `json:"disabled"`
	VMsLimit           int64  `json:"vms_limit"`
	CoresLimit         int64  `json:"cores_limit"`
	RAMLimit           int64  `json:"ram_limit"`
	VolumeCounterLimit int64  `json:"volume_counter_limit"`
	VolumeStorageLimit int64  `json:"volume_storage_limit"`
	IsAdmin            bool   `json:"is_admin"`
	IsMember           bool   `json:"is_member"`
}

// Flavor is a VM size.
type Flavor struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	VCPUs              int64   `json:"vcpus"`
	RAM                int64   `json:"ram"`
	RootDisk           int64   `json:"root_disk"`
	EphemeralDisk      int64   `json:"ephemeral_disk"`
	GPU                int64   `json:"gpu"`
	CreditsCostPerHour float64 `json:"credits_cost_per_hour"`
}

// Image is a base image or snapshot image.
type Image struct {
	OpenstackID        string   `json:"openstack_id"`
	Name               string   `json:"name"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Status             string   `json:"status"`
	OSDistro           string   `json:"os_distro"`
	OSVersion          string   `json:"os_version"`
	Tags               []string `json:"tags"`
	MinRAM             int64    `json:"min_ram"`
	MinDisk            int64    `json:"min_disk"`
	IsSnapshot         bool     `json:"is_snapshot"`
	ResenvTemplateName string   `json:"resenv_template_name"`
	CreatedAt          string   `json:"created_at"`
}

// Playbook is one playbook run of a VM.
type Playbook struct {
	ID   int64 `json:"id"`
	Done bool  `json:"done"`
}

// Playbooks decodes from null, a single object or an array.
type Playbooks []Playbook

// UnmarshalJSON accepts null, a single playbook object or an array of them.
func (p *Playbooks) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch {
	case len(data) == 0 || bytes.Equal(data, []byte("null")):
		*p = nil
		return nil
	case data[0] == '[':
		var list []Playbook
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*p = list
		return nil
	default:
		var one Playbook
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		*p = Playbooks{one}
		return nil
	}
}

// AllDone reports whether every playbook is done (true when there are none).
func (p Playbooks) AllDone() bool {
	return !slices.ContainsFunc(p, func(pb Playbook) bool { return !pb.Done })
}

// VM is a virtual machine.
type VM struct {
	UUID                   string    `json:"uuid"`
	Name                   string    `json:"name"`
	OpenstackID            *string   `json:"openstack_id"`
	VMState                string    `json:"vm_state"`
	TaskState              *string   `json:"task_state"`
	FixedIP                string    `json:"fixed_ip"`
	ResearchEnvironmentURL string    `json:"research_environment_url"`
	ImageName              string    `json:"image_name"`
	Project                *int64    `json:"project"`
	Flavor                 Flavor    `json:"flavor"`
	Playbook               Playbooks `json:"playbook"`
	CreatedAtDate          *string   `json:"created_at_date"`
}

// VolumeMachine is the VM a volume is attached to.
type VolumeMachine struct {
	Name        string  `json:"name"`
	OpenstackID *string `json:"openstack_id"`
	VMState     string  `json:"vm_state"`
}

// Volume is a block storage volume.
type Volume struct {
	UUID           string         `json:"uuid"`
	Name           string         `json:"name"`
	Device         string         `json:"device"`
	Path           string         `json:"path"`
	Status         string         `json:"status"`
	Storage        int64          `json:"storage"`
	OpenstackID    *string        `json:"openstack_id"`
	ProjectID      json.Number    `json:"project_id"`
	VirtualMachine *VolumeMachine `json:"virtual_machine"`
}

// Snapshot is a VM snapshot.
type Snapshot struct {
	Name        string `json:"name"`
	OpenstackID string `json:"openstack_id"`
	Status      string `json:"status"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	ProjectID   int64  `json:"project_id"`
}

// ResearchEnvironmentBackend configures an optional research environment.
type ResearchEnvironmentBackend struct {
	CreateOnly bool   `json:"create_only"`
	Template   string `json:"template"`
	UserPath   string `json:"user_path"`
}

// CreateVMRequest is the body of POST vms/.
// Request shape assumed from the SimpleVM wiki (not in OpenAPI schema).
type CreateVMRequest struct {
	ProjectID           int64                       `json:"project_id"`
	ImageName           string                      `json:"image_name"`
	FlavorName          string                      `json:"flavor_name"`
	VMName              string                      `json:"vm_name"`
	ResearchEnvironment *ResearchEnvironmentBackend `json:"research_environment_backend,omitempty"`
}

// CreateVolumeRequest is the body of POST volumes/.
type CreateVolumeRequest struct {
	ProjectID      int64  `json:"project_id"`
	Storage        int64  `json:"storage"`
	Name           string `json:"name"`
	SourceVolumeID string `json:"source_volume_id,omitempty"`
	VolumeSnapID   string `json:"volume_snap_id,omitempty"`
}

// Volume OS actions for VolumeActionRequest.OSAction.
const (
	VolumeActionAttach = "attach"
	VolumeActionDetach = "detach"
	VolumeActionRename = "rename"
	VolumeActionExtend = "extend"
)

// VolumeActionRequest is the body of POST volumes/{uuid}/action/.
// Request shape assumed from the SimpleVM wiki (not in OpenAPI schema).
type VolumeActionRequest struct {
	OSAction string `json:"os_action"`
	VMUUID   string `json:"vm_uuid,omitempty"`
	NewName  string `json:"new_name,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// CreateSnapshotRequest is the body of POST snapshots/.
type CreateSnapshotRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	VMUUID      string `json:"vm_uuid"`
}

// VMActionResponse is returned by VM create and action calls.
type VMActionResponse struct {
	UUID        string `json:"uuid"`
	OpenstackID string `json:"openstack_id"`
	OSAction    string `json:"os_action"`
	VMState     string `json:"vm_state"`
	TaskState   string `json:"task_state"`
}

// VolumeActionResponse is returned by volume create calls.
type VolumeActionResponse struct {
	UUID         string `json:"uuid"`
	OpenstackID  string `json:"openstack_id"`
	OSAction     string `json:"os_action"`
	VolumeStatus string `json:"volume_status"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
}
