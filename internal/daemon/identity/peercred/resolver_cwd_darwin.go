//go:build darwin

package peercred

/*
#include <stdlib.h>
#include <string.h>
#include <sys/proc_info.h>
#include <libproc.h>

// thrum_proc_cwd retrieves the current working directory of the given pid
// using macOS's libproc API. proc_pidinfo with PROC_PIDVNODEPATHINFO is the
// same path used by `ps`, `lsof`, and Activity Monitor — fast (microseconds,
// not a subprocess) and reliable. Replaces the gopsutil fallback which is
// documented as "not implemented yet" on Darwin (returning that error sent
// the daemon down a legacy client-asserted identity path that trusted stale
// THRUM_AGENT_ID env vars; see thrum-84xc diagnostic thread + thrum-2t7d).
//
// Returns 0 on success with NUL-terminated path written to out (max outsize
// bytes). Returns -1 on failure with errno set; caller treats errno-set
// failures as transient and lets the request fall through to the legacy
// client-asserted path.
static int thrum_proc_cwd(pid_t pid, char *out, size_t outsize) {
    struct proc_vnodepathinfo vpi;
    int size = proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, &vpi, sizeof(vpi));
    if (size <= 0) {
        // errno set by proc_pidinfo
        return -1;
    }
    if (size < (int)sizeof(vpi)) {
        // Short read — vpi is incomplete, cdir.vip_path may be uninitialized.
        return -1;
    }
    if (vpi.pvi_cdir.vip_path[0] == '\0') {
        // Process has no cwd resolution available (rare, e.g. mid-exec).
        return -1;
    }
    size_t plen = strnlen(vpi.pvi_cdir.vip_path, sizeof(vpi.pvi_cdir.vip_path));
    if (plen >= outsize) {
        // Truncation would lose information; treat as failure rather than
        // returning a partial path that won't match a real worktree.
        return -1;
    }
    memcpy(out, vpi.pvi_cdir.vip_path, plen);
    out[plen] = '\0';
    return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// processCWD returns the current working directory of the process with the
// given PID using macOS's libproc API directly. Gopsutil's Cwd() is
// documented as "not implemented yet" on Darwin; that fallback put the
// daemon on a legacy client-asserted-identity path that trusted stale
// THRUM_AGENT_ID env vars and produced cross-worktree identity confusion.
// libproc.proc_pidinfo(PROC_PIDVNODEPATHINFO) is the same path used by ps,
// lsof, and Activity Monitor — fast, reliable, no subprocess overhead.
func processCWD(pid int) (string, error) {
	const maxPathLen = 4096 // PATH_MAX on Darwin (MAXPATHLEN is 1024 but vnode paths can be longer in edge cases)
	buf := make([]byte, maxPathLen)
	rc, err := C.thrum_proc_cwd(
		C.pid_t(pid), //nolint:gosec // pid comes from kernel peer creds, always a valid pid_t
		(*C.char)(unsafe.Pointer(&buf[0])),
		C.size_t(maxPathLen),
	)
	if rc != 0 {
		if err != nil {
			return "", fmt.Errorf("libproc proc_pidinfo PROC_PIDVNODEPATHINFO pid=%d: %w", pid, err)
		}
		return "", fmt.Errorf("libproc proc_pidinfo PROC_PIDVNODEPATHINFO pid=%d: failed (no errno)", pid)
	}
	// buf is NUL-terminated by the C side; find the terminator.
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	if n == 0 {
		return "", fmt.Errorf("libproc proc_pidinfo PROC_PIDVNODEPATHINFO pid=%d: empty path", pid)
	}
	return string(buf[:n]), nil
}
