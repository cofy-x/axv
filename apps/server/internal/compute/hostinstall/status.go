package hostinstall

const (
	UnitName       = "axv-host.service"
	UnitFile       = "/etc/systemd/system/" + UnitName
	BinDir         = "/opt/axv/bin"
	ConfigFile     = "/etc/axv/host.json"
	StandardSocket = "/run/axv-host/control.sock"
)

// InstallOptions controls Linux Firecracker host installation behavior.
type InstallOptions struct {
	Check          bool
	SocketGroup    string
	Enable         bool
	ApplySysctl    bool
	ConnectProfile bool
	ProfileHome    string
}

// UninstallOptions controls how much state Linux host uninstallation removes.
type UninstallOptions struct {
	PurgeConfig bool
	PurgeData   bool
}

// ServiceStatus summarizes the administrator-managed Linux VM host service.
type ServiceStatus struct {
	Supported        bool   `json:"supported"`
	Installed        bool   `json:"installed"`
	Enabled          bool   `json:"enabled"`
	Active           bool   `json:"active"`
	State            string `json:"state"`
	Socket           string `json:"socket,omitempty"`
	SocketReachable  bool   `json:"socketReachable"`
	InstalledBinary  string `json:"installedBinary,omitempty"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	CurrentVersion   string `json:"currentVersion,omitempty"`
	VersionDrift     bool   `json:"versionDrift,omitempty"`
	UnitFile         string `json:"unitFile,omitempty"`
	ConfigFile       string `json:"configFile,omitempty"`
	Detail           string `json:"detail,omitempty"`
}
