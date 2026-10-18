package compute

// Runtime identifies a host and reports controller recovery without probing
// providers. Clients verify it on the same connection used for their request.
type Runtime struct {
	Ready             bool   `json:"ready"`
	HostID            string `json:"hostId"`
	ProtocolVersion   int    `json:"protocolVersion"`
	ConfigFingerprint string `json:"configFingerprint"`
}
