package compute

import "time"

// ProjectVolumeBackup records an offline whole-volume copy. Projects retain
// their original identities as provenance, never as identities for restored work.
type ProjectVolumeBackup struct {
	HostID                string    `json:"hostId"`
	SourceVolumeID        string    `json:"sourceVolumeId"`
	SourceVolumeRevision  int64     `json:"sourceVolumeRevision"`
	SourceMachineID       string    `json:"sourceMachineId"`
	SourceMachineRevision int64     `json:"sourceMachineRevision"`
	Projects              []Project `json:"projects"`
	CreatedAt             time.Time `json:"createdAt"`
}

type BackupProjectRequest struct {
	RequestID       string `json:"requestId"`
	ProjectRevision int64  `json:"projectRevision"`
	VolumeRevision  int64  `json:"volumeRevision"`
	MachineID       string `json:"machineId"`
	MachineRevision int64  `json:"machineRevision"`
	Name            string `json:"name"`
}

type RestoreProjectVolumeRequest struct {
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision"`
	Name      string `json:"name"`
}

type BindRestoredProjectRequest struct {
	RequestID       string `json:"requestId"`
	SourceProjectID string `json:"sourceProjectId"`
	VolumeID        string `json:"volumeId"`
	VolumeRevision  int64  `json:"volumeRevision"`
	MachineID       string `json:"machineId"`
	Name            string `json:"name,omitempty"`
}

func (v Volume) IsProjectBackup() bool { return v.Backup != nil && v.RestoredFrom == "" }
