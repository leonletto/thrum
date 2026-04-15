// Package websocket exports internal helpers for use in external test files.
// This file is compiled only during testing.
package websocket

// AllowedOriginsForPort exposes allowedOriginsForPort for white-box unit tests.
func AllowedOriginsForPort(port int) []string {
	return allowedOriginsForPort(port)
}

// CheckOrigin exposes checkOrigin for white-box unit tests.
func CheckOrigin(allowedOrigins []string, origin string) bool {
	return checkOrigin(allowedOrigins, origin)
}
