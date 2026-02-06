//go:build windows

package commands

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x02
	lockfileFailImmediately = 0x01
	errLockViolation        = syscall.Errno(33)
)

func lockFileNonBlocking(f *os.File) error {
	h := syscall.Handle(f.Fd())
	ol := new(syscall.Overlapped)

	r1, _, err := procLockFileEx.Call(
		uintptr(h),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1, 0,
		uintptr(unsafe.Pointer(ol)),
	)
	if r1 == 0 {
		return err
	}

	return nil
}

func unlockFile(f *os.File) error {
	h := syscall.Handle(f.Fd())
	ol := new(syscall.Overlapped)

	r1, _, err := procUnlockFileEx.Call(
		uintptr(h),
		0,
		1, 0,
		uintptr(unsafe.Pointer(ol)),
	)
	if r1 == 0 {
		return err
	}

	return nil
}

func isLockBusy(err error) bool {
	return errors.Is(err, errLockViolation)
}
