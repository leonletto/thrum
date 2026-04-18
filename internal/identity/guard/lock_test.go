package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicWrite_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := AtomicWrite(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a":1}` {
		t.Errorf("got %q", b)
	}
}

func TestAtomicWrite_ConcurrentSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = AtomicWrite(path, fmt.Appendf(nil, `{"n":%d}`, n))
		}(i)
	}
	wg.Wait()

	// Must end with valid JSON, never a partial/interleaved write.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	if err := json.Unmarshal(b, &v); err != nil {
		t.Errorf("corrupted: %v (bytes=%q)", err, b)
	}
}

func TestAtomicWrite_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected missing, got %v", err)
	}
	if err := AtomicWrite(path, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Errorf("got %q", b)
	}
}
