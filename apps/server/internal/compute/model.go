// Package compute defines shared machine contracts, validation, and provider
// capabilities. Its subpackages own host control, persistence, and transport.
package compute

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
)

const ProtocolVersion = 3

type Error struct {
	Code    string `json:"code"`
	Message string `json:"error"`
}

func (e *Error) Error() string { return e.Message }

func Problem(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "internal_error"
}

func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

type Resources struct {
	CPUs      int   `json:"cpus"`
	MemoryMiB int64 `json:"memoryMiB"`
	DiskGiB   int64 `json:"diskGiB"`
}

type Capacity struct {
	Resources
	Machines int `json:"machines"`
	Workers  int `json:"workers"`
}

// Image paths and hashes are administrator-controlled, never accepted from a
// machine creation request. Images are checked before use, not downloaded on boot.
type Image struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Provider     string               `json:"provider"`
	Arch         string               `json:"arch"`
	Rootfs       string               `json:"rootfs"`
	RootfsSHA256 string               `json:"rootfsSHA256"`
	Kernel       string               `json:"kernel,omitempty"`
	KernelSHA256 string               `json:"kernelSHA256,omitempty"`
	Guest        *guestimage.Contract `json:"guest,omitempty"`
	MinRootGiB   int64                `json:"minRootGiB,omitempty"`
}

type ImageInfo struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Provider   string               `json:"provider"`
	Arch       string               `json:"arch"`
	Status     string               `json:"status,omitempty"`
	Guest      *guestimage.Contract `json:"guest,omitempty"`
	Managed    bool                 `json:"managed"`
	Revision   int64                `json:"revision"`
	SHA256     string               `json:"sha256,omitempty"`
	SizeBytes  int64                `json:"sizeBytes,omitempty"`
	MinRootGiB int64                `json:"minRootGiB,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	Source     string               `json:"source,omitempty"`
}

type Spec struct {
	Name         string    `json:"name"`
	Provider     string    `json:"provider"`
	ImageID      string    `json:"imageId"`
	Kind         string    `json:"kind"`
	Resources    Resources `json:"resources"`
	ProjectGiB   int64     `json:"projectGiB"`
	VolumeID     string    `json:"volumeId,omitempty"`
	RetainVolume bool      `json:"retainVolume"`
	TTLSeconds   int64     `json:"ttlSeconds,omitempty"`
}

type CreateRequest struct {
	RequestID string `json:"requestId"`
	Spec      Spec   `json:"spec"`
	Start     bool   `json:"start"`
}

type ActionRequest struct {
	PackageSource string `json:"packageSource,omitempty"`
	RequestID     string `json:"requestId"`
	Revision      int64  `json:"revision"`
	Action        string `json:"action"`
}

type UpdateRequest struct {
	RequestID  string `json:"requestId"`
	Revision   int64  `json:"revision"`
	Spec       Spec   `json:"spec"`
	RenewLease bool   `json:"renewLease,omitempty"`
}

type Machine struct {
	ID                string     `json:"id"`
	HostID            string     `json:"hostId"`
	Spec              Spec       `json:"spec"`
	PendingSpec       *Spec      `json:"pendingSpec,omitempty"`
	Revision          int64      `json:"revision"`
	Desired           string     `json:"desired"`
	Status            string     `json:"status"`
	Ready             bool       `json:"ready"`
	GuestState        string     `json:"guestState"`
	GuestReason       string     `json:"guestReason,omitempty"`
	GuestCapabilities []string   `json:"guestCapabilities"`
	Reason            string     `json:"reason,omitempty"`
	Reserved          bool       `json:"reserved"`
	Slot              int        `json:"slot"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
}

type Volume struct {
	Backup           *ProjectVolumeBackup `json:"backup,omitempty"`
	RestoredFrom     string               `json:"restoredFrom,omitempty"`
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Revision         int64                `json:"revision"`
	Provider         string               `json:"provider"`
	SizeGiB          int64                `json:"sizeGiB"`
	PendingSizeGiB   int64                `json:"pendingSizeGiB,omitempty"`
	TransientGiB     int64                `json:"transientGiB,omitempty"`
	PendingAction    string               `json:"pendingAction,omitempty"`
	PendingMachineID string               `json:"pendingMachineId,omitempty"`
	Retain           bool                 `json:"retain"`
	MachineID        string               `json:"machineId,omitempty"`
	Status           string               `json:"status"`
	Reason           string               `json:"reason,omitempty"`
	CreatedAt        time.Time            `json:"createdAt"`
}

type Operation struct {
	PackageSource    string     `json:"packageSource,omitempty"`
	ID               string     `json:"id"`
	RequestID        string     `json:"requestId"`
	MachineID        string     `json:"machineId"`
	VolumeID         string     `json:"volumeId,omitempty"`
	ResourceID       string     `json:"resourceId,omitempty"`
	ResourceKind     string     `json:"resourceKind,omitempty"`
	Locks            []string   `json:"locks,omitempty"`
	Action           string     `json:"action"`
	Status           string     `json:"status"`
	Error            string     `json:"error,omitempty"`
	Phase            string     `json:"phase,omitempty"`
	Configuration    *Spec      `json:"configuration,omitempty"`
	ResourcesChanged bool       `json:"resourcesChanged,omitempty"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type Result struct {
	Machine   Machine   `json:"machine"`
	Operation Operation `json:"operation"`
}

type Observation struct {
	Status            string
	Ready             bool
	GuestState        string
	GuestReason       string
	GuestCapabilities []string
	Reason            string
	Volume            *Volume
	Image             *ImageRecord
}

type Capability struct {
	Provider          string `json:"provider"`
	Available         bool   `json:"available"`
	Reason            string `json:"reason,omitempty"`
	PersistentVolumes bool   `json:"persistentVolumes"`
	Snapshots         bool   `json:"snapshots"`
	GuestExecution    bool   `json:"guestExecution"`
	Version           string `json:"version,omitempty"`
	Configuration     bool   `json:"configuration"`
	ForceStop         bool   `json:"forceStop"`
	SnapshotReason    string `json:"snapshotReason,omitempty"`
	Volumes           bool   `json:"volumes"`
	VolumeCopy        bool   `json:"volumeCopy"`
	Images            bool   `json:"images"`
}

type ImageRecord struct {
	Image       Image     `json:"image"`
	Status      string    `json:"status"`
	Managed     bool      `json:"managed"`
	Revision    int64     `json:"revision"`
	SizeBytes   int64     `json:"sizeBytes"`
	ReservedGiB int64     `json:"reservedGiB"`
	Source      string    `json:"source,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UploadPath  string    `json:"uploadPath,omitempty"`
}

type ImageRequest struct {
	RequestID string               `json:"requestId"`
	Name      string               `json:"name"`
	Arch      string               `json:"arch"`
	URL       string               `json:"url,omitempty"`
	SHA256    string               `json:"sha256"`
	Guest     *guestimage.Contract `json:"guest,omitempty"`
}

type ImageResult struct {
	Image     ImageInfo `json:"image"`
	Operation Operation `json:"operation"`
}

type CreateVolumeRequest struct {
	RequestID      string `json:"requestId"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	SizeGiB        int64  `json:"sizeGiB"`
	Retain         bool   `json:"retain"`
	SourceVolumeID string `json:"sourceVolumeId,omitempty"`
}

type VolumeRequest struct {
	RequestID       string `json:"requestId"`
	Revision        int64  `json:"revision"`
	Action          string `json:"action"`
	Name            string `json:"name,omitempty"`
	SizeGiB         int64  `json:"sizeGiB,omitempty"`
	Retain          bool   `json:"retain"`
	MachineID       string `json:"machineId,omitempty"`
	MachineRevision int64  `json:"machineRevision,omitempty"`
}

type VolumeResult struct {
	Volume    Volume    `json:"volume"`
	Operation Operation `json:"operation"`
}

func (record ImageRecord) Info() ImageInfo {
	return ImageInfo{ID: record.Image.ID, Name: record.Image.Name, Provider: record.Image.Provider, Arch: record.Image.Arch, Status: record.Status, Managed: record.Managed, Revision: record.Revision, SHA256: record.Image.RootfsSHA256, SizeBytes: record.SizeBytes, MinRootGiB: record.Image.MinRootGiB, Guest: record.Image.Guest, Reason: record.Reason, Source: record.Source}
}
