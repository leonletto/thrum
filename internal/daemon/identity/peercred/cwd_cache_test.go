//go:build unix

package peercred

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCachedProcessCWD_CachesWithinTTL guards thrum-xir.45: the second
// call for the same PID within the cache TTL must reuse the cached
// result, not re-invoke processCWDFn (the expensive lsof shell-out on
// Darwin). This is the primary cache contract — without it, every RPC
// repeats the ~30-300ms subprocess work.
func TestCachedProcessCWD_CachesWithinTTL(t *testing.T) {
	clearCWDCacheForTest()
	var calls int32
	restore := SetProcessCWDFnForTest(func(_ int) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "/test/cwd", nil
	})
	defer restore()

	cwd1, err := cachedProcessCWD(4242)
	if err != nil || cwd1 != "/test/cwd" {
		t.Fatalf("first call: got (%q, %v), want (\"/test/cwd\", nil)", cwd1, err)
	}
	cwd2, err := cachedProcessCWD(4242)
	if err != nil || cwd2 != "/test/cwd" {
		t.Fatalf("second call: got (%q, %v), want (\"/test/cwd\", nil)", cwd2, err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("processCWDFn called %d times; cache should have served the second lookup → want 1", got)
	}
}

// TestCachedProcessCWD_ExpiredEntryFallsThrough confirms TTL-based
// eviction works: after the entry expires, the next call re-invokes
// processCWDFn. Uses a synthetically-short TTL to avoid real-time sleep.
func TestCachedProcessCWD_ExpiredEntryFallsThrough(t *testing.T) {
	clearCWDCacheForTest()
	originalTTL := cwdCacheTTL
	cwdCacheTTL = 1 * time.Millisecond
	defer func() { cwdCacheTTL = originalTTL }()

	var calls int32
	restore := SetProcessCWDFnForTest(func(_ int) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "/test/cwd", nil
	})
	defer restore()

	if _, err := cachedProcessCWD(7777); err != nil {
		t.Fatalf("first call err: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := cachedProcessCWD(7777); err != nil {
		t.Fatalf("second call err: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("processCWDFn called %d times; expired entry should have fallen through → want 2", got)
	}
}

// TestCachedProcessCWD_DifferentPIDsAreIsolated confirms two distinct
// PIDs don't share a cache entry — each gets its own cwd. Guards against
// a wrong-keying bug where the cache might collapse multiple PIDs into
// one slot.
func TestCachedProcessCWD_DifferentPIDsAreIsolated(t *testing.T) {
	clearCWDCacheForTest()
	restore := SetProcessCWDFnForTest(func(pid int) (string, error) {
		switch pid {
		case 100:
			return "/cwd/one", nil
		case 200:
			return "/cwd/two", nil
		}
		return "", errors.New("unexpected pid")
	})
	defer restore()

	cwd1, _ := cachedProcessCWD(100)
	cwd2, _ := cachedProcessCWD(200)
	cwd1Again, _ := cachedProcessCWD(100)

	if cwd1 != "/cwd/one" || cwd2 != "/cwd/two" || cwd1Again != "/cwd/one" {
		t.Errorf("got (%q, %q, %q), want (\"/cwd/one\", \"/cwd/two\", \"/cwd/one\")", cwd1, cwd2, cwd1Again)
	}
}

// TestCachedProcessCWD_NegativeResultIsCached confirms that an error
// from processCWDFn is also cached (with the same TTL). Repeatedly
// shelling out for a PID we already know is unresolvable would defeat
// the cache's purpose under sustained load.
func TestCachedProcessCWD_NegativeResultIsCached(t *testing.T) {
	clearCWDCacheForTest()
	var calls int32
	wantErr := errors.New("lsof: pid 9999 not found")
	restore := SetProcessCWDFnForTest(func(_ int) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", wantErr
	})
	defer restore()

	_, err1 := cachedProcessCWD(9999)
	_, err2 := cachedProcessCWD(9999)

	if err1 == nil || err2 == nil {
		t.Fatalf("expected non-nil errors on both calls, got (%v, %v)", err1, err2)
	}
	if !errors.Is(err1, wantErr) || !errors.Is(err2, wantErr) {
		t.Errorf("error not preserved through cache: got (%v, %v)", err1, err2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("processCWDFn called %d times for negative-result lookup; want 1 (error must be cached)", got)
	}
}

// TestCachedProcessCWD_ConcurrentSamePIDIsSafe is a goroutine-race smoke
// test: many concurrent lookups for the same PID must not corrupt the
// cache. The exact number of underlying processCWDFn calls is not
// asserted (a small burst before the first writer commits could legitimately
// call it more than once); the property under test is correctness +
// no-data-race.
func TestCachedProcessCWD_ConcurrentSamePIDIsSafe(t *testing.T) {
	clearCWDCacheForTest()
	restore := SetProcessCWDFnForTest(func(_ int) (string, error) {
		return "/test/cwd", nil
	})
	defer restore()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	results := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			cwd, _ := cachedProcessCWD(5555)
			results[idx] = cwd
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got != "/test/cwd" {
			t.Errorf("goroutine %d got cwd %q, want \"/test/cwd\"", i, got)
		}
	}
}
