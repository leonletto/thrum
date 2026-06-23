package gitctx_test

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// assertWallClock is the shared guard for the package's wall-clock perf
// assertions (thrum-1iwi). Wall-clock asserts measure the HOST under load,
// not the code: the 500ms targets fail at 724ms-3.7s on a dogfood box running
// gates/trustd bursts while passing on any quiet host. Shape chosen
// (flaky-tests dispatch item 1): measure always; when the assertion WOULD
// FAIL, skip — visibly, with the loadavg in the message — only if the host
// is demonstrably saturated (1-min loadavg >= NumCPU). This can never
// silently-always-skip: on a quiet host a regression still fails, and on a
// loaded host the skip is loud and diagnosable. THRUM_PERF_STRICT=1 disables
// the skip entirely (CI pinning / quiet-host enforcement).
func assertWallClock(t *testing.T, elapsed, limit time.Duration) {
	t.Helper()
	t.Logf("ExtractWorkContext completed in %v (limit %v)", elapsed, limit)
	if elapsed <= limit {
		return
	}
	if os.Getenv("THRUM_PERF_STRICT") != "1" {
		// max(load1, load5): the 1-min average lags fresh bursts (observed:
		// a 630ms failure slipped through while load1 was still climbing),
		// while the 5-min average carries the chronic background noise
		// (trustd/ReportCrash/ecosystemd on the dogfood box). Either at/above
		// the core count means wall-clock is measuring the host, not the code.
		if load, ok := loadAvgMax(); ok && load >= float64(runtime.NumCPU()) {
			t.Skipf("took %v (> %v) under loadavg %.2f on %d cores — wall-clock not meaningful on a saturated host; set THRUM_PERF_STRICT=1 to force the assertion", elapsed, limit, load, runtime.NumCPU())
		}
	}
	t.Errorf("ExtractWorkContext took too long: %v (expected < %v)", elapsed, limit)
}

// loadAvgMax returns max(1-min, 5-min) host load average. ok=false when the
// platform value can't be determined (the caller then never skips —
// fail-strict).
func loadAvgMax() (float64, bool) {
	var fields []string
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0, false
		}
		fields = strings.Fields(string(data))
	case "darwin":
		out, err := exec.Command("/usr/sbin/sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			// PATH-independent absolute path first; fall back to lookup.
			if out, err = exec.Command("sysctl", "-n", "vm.loadavg").Output(); err != nil {
				return 0, false
			}
		}
		// Format: "{ 1.23 1.45 1.60 }"
		fields = strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
	default:
		return 0, false
	}
	if len(fields) < 2 {
		return 0, false
	}
	l1, err1 := strconv.ParseFloat(fields[0], 64)
	l5, err5 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err5 != nil {
		return 0, false
	}
	return max(l1, l5), true
}
