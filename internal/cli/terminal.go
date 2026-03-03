package cli

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// GetTerminalWidth returns the width of the terminal in columns.
// It tries the following methods in order:
// 1. IOCTL syscall to get terminal dimensions
// 2. COLUMNS environment variable
// 3. Default to 80 columns.
func GetTerminalWidth() int {
	// Try IOCTL syscall first (Unix-like systems)
	if width := getWidthFromIOCTL(); width > 0 {
		return width
	}

	// Try COLUMNS environment variable
	if width := getWidthFromEnv(); width > 0 {
		return width
	}

	// Default fallback
	return 80
}

// getWidthFromIOCTL uses syscall to get terminal dimensions via IOCTL.
func getWidthFromIOCTL() int {
	type winsize struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}

	ws := &winsize{}
	retCode, _, _ := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout), // #nosec G115 -- syscall.Stdout is the constant 1; int->uintptr conversion cannot overflow
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws))) // #nosec G103 -- unsafe.Pointer required for IOCTL syscall to pass winsize struct to kernel; standard Unix pattern //nolint:gosec

	if int(retCode) == -1 { // #nosec G115 -- uintptr->int conversion is intentional sign-extension to check for syscall error return (-1)
		return 0
	}

	return int(ws.Col)
}

// getWidthFromEnv reads the COLUMNS environment variable.
func getWidthFromEnv() int {
	if colStr := os.Getenv("COLUMNS"); colStr != "" {
		if width, err := strconv.Atoi(colStr); err == nil && width > 0 {
			return width
		}
	}
	return 0
}
