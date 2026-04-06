package config

// PeersConfig holds peer connection settings.
type PeersConfig struct {
	AutoConnect       bool `json:"auto_connect"`
	PairingCodeLength int  `json:"pairing_code_length"`
}

// DefaultPeersConfig returns the default peers configuration.
func DefaultPeersConfig() PeersConfig {
	return PeersConfig{
		AutoConnect:       true,
		PairingCodeLength: 16,
	}
}
