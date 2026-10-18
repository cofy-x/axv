package compute

// CreationDefaults is shared by interactive and command-line machine creation.
// Callers apply the selected image's minimum root size and validate capacity.
type CreationDefaults struct {
	Spec             Spec  `json:"spec"`
	Start            bool  `json:"start"`
	TaskTTLSeconds   int64 `json:"taskTtlSeconds"`
	TaskRetainVolume bool  `json:"taskRetainVolume"`
}

// Info is the public host inventory and creation contract returned by /v1/info.
type Info struct {
	ConfigFingerprint string           `json:"configFingerprint"`
	Configured        bool             `json:"configured"`
	HostID            string           `json:"hostId"`
	ProtocolVersion   int              `json:"protocolVersion"`
	HostArch          string           `json:"hostArch"`
	Capacity          Capacity         `json:"capacity"`
	Providers         []Capability     `json:"providers"`
	Images            []ImageInfo      `json:"images"`
	Defaults          CreationDefaults `json:"defaults"`
	Experimental      bool             `json:"experimental"`
}

func DefaultCreationSpec(images []ImageInfo, providers []Capability, arch string) CreationDefaults {
	defaults := CreationDefaults{
		Spec:  Spec{Name: "my-vm", Kind: "development", Resources: Resources{CPUs: 2, MemoryMiB: 1024, DiskGiB: 8}, ProjectGiB: 4, RetainVolume: true},
		Start: true, TaskTTLSeconds: 3600,
	}
	for _, image := range images {
		if image.Arch != arch || (image.Status != "configured" && image.Status != "ready") {
			continue
		}
		for _, provider := range providers {
			if provider.Provider == image.Provider && provider.Available {
				defaults.Spec.Provider, defaults.Spec.ImageID = image.Provider, image.ID
				return defaults
			}
		}
	}
	return defaults
}
