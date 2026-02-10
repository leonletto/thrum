package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestFindAvailablePort(t *testing.T) {
	t.Run("finds available port in range", func(t *testing.T) {
		port, err := FindAvailablePort(9000, 9100)
		if err != nil {
			t.Fatalf("expected to find available port, got error: %v", err)
		}
		if port < 9000 || port > 9100 {
			t.Errorf("port %d outside expected range 9000-9100", port)
		}
	})

	t.Run("returns first available port", func(t *testing.T) {
		// Occupy a port to verify it tries the next one
		listener, err := net.Listen("tcp", "localhost:9050")
		if err != nil {
			t.Skipf("could not occupy port 9050 for test: %v", err)
		}
		defer func() { _ = listener.Close() }()

		// Should skip 9050 and find another port
		port, err := FindAvailablePort(9050, 9060)
		if err != nil {
			t.Fatalf("expected to find available port, got error: %v", err)
		}
		if port == 9050 {
			t.Error("should not return occupied port 9050")
		}
		if port < 9050 || port > 9060 {
			t.Errorf("port %d outside expected range 9050-9060", port)
		}
	})

	t.Run("error when min > max", func(t *testing.T) {
		_, err := FindAvailablePort(9100, 9000)
		if err == nil {
			t.Error("expected error when min > max")
		}
	})

	t.Run("error when range invalid", func(t *testing.T) {
		_, err := FindAvailablePort(0, 100)
		if err == nil {
			t.Error("expected error when min < 1")
		}

		_, err = FindAvailablePort(65530, 65540)
		if err == nil {
			t.Error("expected error when max > 65535")
		}
	})

	t.Run("error when all ports occupied", func(t *testing.T) {
		// Create listeners on a small range
		var listeners []net.Listener
		for i := 9200; i <= 9202; i++ {
			l, err := net.Listen("tcp", "localhost:"+string(rune('0'+i-9200+48))+"920"+string(rune('0'+i%10)))
			if err == nil {
				listeners = append(listeners, l)
			}
		}
		defer func() {
			for _, l := range listeners {
				_ = l.Close()
			}
		}()

		// Actually occupy ports 9200-9202
		listeners = nil
		for port := 9200; port <= 9202; port++ {
			l, err := net.Listen("tcp", "localhost:"+itoa(port))
			if err != nil {
				t.Skipf("could not occupy port %d for test: %v", port, err)
			}
			listeners = append(listeners, l)
		}

		_, err := FindAvailablePort(9200, 9202)
		if err == nil {
			t.Error("expected error when all ports occupied")
		}
	})
}

func itoa(n int) string {
	return string([]byte{
		byte('0' + n/1000%10),
		byte('0' + n/100%10),
		byte('0' + n/10%10),
		byte('0' + n%10),
	})
}

func TestIsPortAvailable(t *testing.T) {
	t.Run("returns true for available port", func(t *testing.T) {
		// Find an available port first
		port, err := FindAvailablePort(9300, 9400)
		if err != nil {
			t.Skipf("could not find available port: %v", err)
		}

		if !isPortAvailable(port) {
			t.Errorf("port %d should be available", port)
		}
	})

	t.Run("returns false for occupied port", func(t *testing.T) {
		listener, err := net.Listen("tcp", "localhost:9350")
		if err != nil {
			t.Skipf("could not occupy port 9350 for test: %v", err)
		}
		defer func() { _ = listener.Close() }()

		if isPortAvailable(9350) {
			t.Error("port 9350 should not be available")
		}
	})
}

func TestWritePortFile(t *testing.T) {
	t.Run("writes port to file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		err := WritePortFile(path, 9999)
		if err != nil {
			t.Fatalf("WritePortFile failed: %v", err)
		}

		content, err := os.ReadFile(path) //nolint:gosec // G304 - test fixture path
		if err != nil {
			t.Fatalf("failed to read port file: %v", err)
		}

		if string(content) != "9999\n" {
			t.Errorf("expected '9999\\n', got %q", string(content))
		}
	})

	t.Run("creates parent directory", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "subdir", "nested", "test.port")

		err := WritePortFile(path, 8080)
		if err != nil {
			t.Fatalf("WritePortFile failed: %v", err)
		}

		if _, err := os.Stat(path); err != nil {
			t.Errorf("port file was not created: %v", err)
		}
	})

	t.Run("atomic write", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		// Write initial value
		err := WritePortFile(path, 9000)
		if err != nil {
			t.Fatalf("WritePortFile failed: %v", err)
		}

		// Overwrite with new value
		err = WritePortFile(path, 9001)
		if err != nil {
			t.Fatalf("WritePortFile failed: %v", err)
		}

		// Verify no temp file remains
		tempPath := path + ".tmp"
		if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
			t.Error("temporary file should not exist after successful write")
		}

		// Verify correct content
		port, err := ReadPortFile(path)
		if err != nil {
			t.Fatalf("ReadPortFile failed: %v", err)
		}
		if port != 9001 {
			t.Errorf("expected port 9001, got %d", port)
		}
	})
}

func TestReadPortFile(t *testing.T) {
	t.Run("reads valid port", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		if err := os.WriteFile(path, []byte("9999\n"), 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		port, err := ReadPortFile(path)
		if err != nil {
			t.Fatalf("ReadPortFile failed: %v", err)
		}
		if port != 9999 {
			t.Errorf("expected port 9999, got %d", port)
		}
	})

	t.Run("reads port without trailing newline", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		if err := os.WriteFile(path, []byte("8080"), 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		port, err := ReadPortFile(path)
		if err != nil {
			t.Fatalf("ReadPortFile failed: %v", err)
		}
		if port != 8080 {
			t.Errorf("expected port 8080, got %d", port)
		}
	})

	t.Run("error for non-existent file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nonexistent.port")

		_, err := ReadPortFile(path)
		if err == nil {
			t.Error("expected error for non-existent file")
		}
		if !os.IsNotExist(err) {
			t.Errorf("expected IsNotExist error, got: %v", err)
		}
	})

	t.Run("error for invalid content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		if err := os.WriteFile(path, []byte("not-a-number\n"), 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err := ReadPortFile(path)
		if err == nil {
			t.Error("expected error for invalid content")
		}
	})

	t.Run("error for port out of range", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		// Test port too high
		if err := os.WriteFile(path, []byte("65536\n"), 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err := ReadPortFile(path)
		if err == nil {
			t.Error("expected error for port out of range")
		}

		// Test port too low
		if err := os.WriteFile(path, []byte("0\n"), 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err = ReadPortFile(path)
		if err == nil {
			t.Error("expected error for port 0")
		}
	})
}

func TestRemovePortFile(t *testing.T) {
	t.Run("removes existing file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		if err := WritePortFile(path, 9999); err != nil {
			t.Fatalf("WritePortFile failed: %v", err)
		}

		err := RemovePortFile(path)
		if err != nil {
			t.Fatalf("RemovePortFile failed: %v", err)
		}

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("file should not exist after removal")
		}
	})

	t.Run("no error for non-existent file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nonexistent.port")

		err := RemovePortFile(path)
		if err != nil {
			t.Errorf("RemovePortFile should not error for non-existent file: %v", err)
		}
	})
}

func TestRoundTrip(t *testing.T) {
	t.Run("write and read port", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.port")

		// Find and write a port
		port, err := FindAvailablePort(9400, 9500)
		if err != nil {
			t.Fatalf("FindAvailablePort failed: %v", err)
		}

		if err := WritePortFile(path, port); err != nil {
			t.Fatalf("WritePortFile failed: %v", err)
		}

		// Read it back
		readPort, err := ReadPortFile(path)
		if err != nil {
			t.Fatalf("ReadPortFile failed: %v", err)
		}

		if readPort != port {
			t.Errorf("expected port %d, got %d", port, readPort)
		}

		// Remove it
		if err := RemovePortFile(path); err != nil {
			t.Fatalf("RemovePortFile failed: %v", err)
		}
	})
}
