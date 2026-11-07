package secret

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// statusKiB is a line of /proc/self/status.
func statusKiB(name string) (uint64, error) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	m := regexp.MustCompile(`(?m)^` + name + `:\s+(\d+) kB$`).FindSubmatch(b)
	if m == nil {
		return 0, fmt.Errorf("no %s in /proc/self/status", name)
	}
	return strconv.ParseUint(string(m[1]), 10, 64)
}

// TestMain runs the lock's checks in a process of their own, started again by the
// tests below: a lock cannot be taken back.
func TestMain(m *testing.M) {
	if mode := os.Getenv("SECRET_TEST_LOCK"); mode != "" {
		os.Exit(lockChecks(mode))
	}
	os.Exit(m.Run())
}

// lockChecks is the process of the lock's tests:
//
//   - refused: a low limit, no capability; the lock is refused, nothing is locked, and
//     the process is not dumpable all the same.
//   - covers: the whole process is locked and not dumpable.
func lockChecks(mode string) int {
	fail := func(format string, args ...any) int {
		os.Stderr.WriteString("FAIL: " + fmt.Sprintf(format, args...) + "\n")
		return 1
	}
	if mode == "refused" {
		if err := unix.Setrlimit(unix.RLIMIT_MEMLOCK, &unix.Rlimit{Cur: 1 << 16, Max: 1 << 16}); err != nil {
			return fail("the limit: %v", err)
		}
	}
	err := LockProcess()
	if d, derr := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); derr != nil || d != 0 {
		return fail("dumpable: %d %v, want 0", d, derr)
	}
	locked, serr := statusKiB("VmLck")
	if serr != nil {
		return fail("%v", serr)
	}
	if mode == "refused" {
		if err == nil {
			return fail("the lock was taken under the limit, without the capability")
		}
		if !errors.Is(err, unix.EPERM) || !strings.Contains(err.Error(), "CAP_IPC_LOCK") {
			return fail("the refusal: %v", err)
		}
		var limit unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &limit); err != nil || limit.Cur != 1<<16 {
			return fail("the limit is left at %d (%v)", limit.Cur, err)
		}
		if locked != 0 {
			return fail("VmLck %d kB after a refusal", locked)
		}
		// No mapping past the limit fails: the runtime lives on.
		b := make([]byte, 64<<20)
		b[len(b)-1] = 1
		runtime.KeepAlive(b)
		return 0
	}
	if err != nil {
		return fail("LockProcess: %v", err)
	}
	size, serr := statusKiB("VmSize")
	if serr != nil {
		return fail("%v", serr)
	}
	// MCL_ONFAULT: what is touched is locked, the reserve is not resident.
	b := make([]byte, 32<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	runtime.KeepAlive(b)
	if locked, _ = statusKiB("VmLck"); locked < 32<<10 {
		return fail("VmLck %d kB of VmSize %d kB with 32 MiB touched", locked, size)
	}
	return 0
}

// A limit without the capability is refused, in a user namespace of its own: uid 0
// there has no CAP_IPC_LOCK where the kernel asks, as a container's root has not.
func TestLockProcessRefused(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "SECRET_TEST_LOCK=refused")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}}
	out, err := cmd.CombinedOutput()
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EINVAL) {
		t.Skipf("no user namespace here: %v", err)
	}
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// The whole process is locked. It takes CAP_IPC_LOCK or no limit; a user's test run
// has neither, root on a VM has.
func TestLockProcessCovers(t *testing.T) {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &limit); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 && limit.Cur != unix.RLIM_INFINITY {
		t.Skipf("a limit of %d bytes and no CAP_IPC_LOCK: as root, on a VM", limit.Cur)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "SECRET_TEST_LOCK=covers")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestWipe(t *testing.T) {
	b := []byte("a secret of some length")
	view := b[:3] // a shorter view wipes the whole capacity
	Wipe(view)
	for i, c := range b {
		if c != 0 {
			t.Fatalf("byte %d is %#x after Wipe", i, c)
		}
	}
	Wipe(nil) // nothing to wipe is no error
}

func TestLockedBuffer(t *testing.T) {
	for _, n := range []int{0, -1} {
		if b, err := LockedBuffer(n); err == nil || b != nil {
			t.Errorf("LockedBuffer(%d) = %v, %v; want an error", n, b, err)
		}
	}
	before, err := statusKiB("VmLck")
	if err != nil {
		t.Fatal(err)
	}
	b, err := LockedBuffer(512)
	if b == nil || len(b) != 0 || cap(b) != 512 {
		t.Fatalf("LockedBuffer(512): len %d cap %d", len(b), cap(b))
	}
	var limit unix.Rlimit
	if gerr := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &limit); gerr != nil {
		t.Fatal(gerr)
	}
	if err != nil {
		if limit.Cur >= 1<<16 {
			t.Fatalf("not locked under a limit of %d bytes: %v", limit.Cur, err)
		}
		t.Skipf("no room to lock: %v", err)
	}
	after, _ := statusKiB("VmLck")
	if after <= before {
		t.Errorf("VmLck %d kB before, %d kB after", before, after)
	}
}
