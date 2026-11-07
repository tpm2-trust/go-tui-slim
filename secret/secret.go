// Package secret keeps what a program must not leak out of reach: it wipes a secret
// once used, reads one into memory that is locked, and locks the whole process and
// makes it not dumpable. A secret is held in a []byte, never in a string, which can
// be neither wiped nor told where to live.
package secret

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// Wipe overwrites b where it is, to its full capacity, in case a shorter view of it
// is handed in.
func Wipe(b []byte) {
	b = b[:cap(b)]
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// LockedBuffer is an empty buffer of capacity n whose memory is locked: it never
// reaches swap. The buffer is always returned; an error says that it is not locked,
// and the caller decides whether to go on with it. The lock lasts as long as the
// process, since the pages may hold other memory too; the caller wipes the buffer
// when the secret is used.
func LockedBuffer(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("a locked buffer of %d bytes", n)
	}
	b := make([]byte, 0, n)
	if err := unix.Mlock(b[:n]); err != nil {
		return b, fmt.Errorf("the buffer is not locked in memory: %w", err)
	}
	return b, nil
}

// lockFlags lock every page of the process, now and to come, when it is first
// touched. MCL_ONFAULT is what keeps a Go process's memory its own to give back:
//
//   - The runtime reserves more than a gigabyte it never touches, and maps its
//     heap ahead of its use. Without MCL_ONFAULT all that is mapped readable is
//     made resident at once.
//   - The kernel refuses MADV_DONTNEED on locked memory, so the runtime gives it
//     back by mapping over it. Without MCL_ONFAULT, MCL_FUTURE makes that new
//     mapping resident too.
//
// What is locked is the same: a page cannot be evicted from its first touch to its
// end. Only a page already in swap when the lock is taken stays there until
// touched, which is why the lock comes before any secret.
const lockFlags = unix.MCL_CURRENT | unix.MCL_FUTURE | unix.MCL_ONFAULT

// LockProcess keeps what this process holds out of swap and core files: it is not
// dumpable, and its memory is locked. Call it first thing, before any secret is in
// memory. An error means the memory is not locked; the process is not dumpable all
// the same, unless that is the error.
//
// Both take the whole process: mlockall and PR_SET_DUMPABLE are the address space's,
// RLIMIT_MEMLOCK the thread group's. The lock does not cross an exec.
//
// Without CAP_IPC_LOCK any finite limit on locked memory is refused, however large:
// under MCL_FUTURE a mapping past the limit fails, and for the Go runtime that is
// fatal, at no place the program could answer. So the kernel is asked first: with
// the soft limit at 0, mlockall succeeds only with the capability. An unlimited
// RLIMIT_MEMLOCK (LimitMEMLOCK=infinity, --ulimit memlock=-1) takes the lock without
// it.
func LockProcess() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("not dumpable: %w", err)
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &limit); err != nil {
		return fmt.Errorf("the limit on locked memory: %w", err)
	}
	if limit.Cur == unix.RLIM_INFINITY {
		if err := unix.Mlockall(lockFlags); err != nil {
			return fmt.Errorf("the memory is not locked: %w", err)
		}
		return nil
	}
	zero := unix.Rlimit{Cur: 0, Max: limit.Max}
	if err := unix.Setrlimit(unix.RLIMIT_MEMLOCK, &zero); err != nil {
		return fmt.Errorf("the limit on locked memory: %w", err)
	}
	err := unix.Mlockall(lockFlags)
	if serr := unix.Setrlimit(unix.RLIMIT_MEMLOCK, &limit); serr != nil && err == nil {
		err = serr
	}
	if err != nil {
		return fmt.Errorf("the memory is not locked: locked memory is limited to %d bytes (RLIMIT_MEMLOCK) and this process has no CAP_IPC_LOCK: %w", limit.Cur, err)
	}
	return nil
}
