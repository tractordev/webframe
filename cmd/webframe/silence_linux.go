package main

import (
	"syscall"
	"unsafe"
)

func dup2(from, to int) error { return syscall.Dup3(from, to, 0) }

func init() {
	// JavaScriptCore's GC takes over SIGUSR1 and warns when it isn't SIG_DFL,
	// which it never is since the Go runtime installs a handler for every
	// signal. Reset it behind the runtime's back (it only reinstalls on
	// signal.Notify) so the takeover is silent. JSC_SIGNAL_FOR_GC can't be
	// used instead: JSC also parses JSC_* variables as engine options and
	// complains about it.
	var dfl [4]uint64 // struct sigaction: SIG_DFL, no flags, empty mask
	_, _, _ = syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(syscall.SIGUSR1),
		uintptr(unsafe.Pointer(&dfl)), 0, 8, 0, 0)

	// Quiet GTK/WebKit warnings about the accessibility bus and EGL.
	for k, v := range map[string]string{"NO_AT_BRIDGE": "1", "EGL_LOG_LEVEL": "fatal"} {
		if _, ok := syscall.Getenv(k); !ok {
			_ = syscall.Setenv(k, v)
		}
	}
}
