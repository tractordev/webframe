package main

import (
	"os"
	"syscall"
)

// silence points the process's stdout and stderr at NUL, including what
// native libraries write there.
func silence() {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	set := syscall.NewLazyDLL("kernel32.dll").NewProc("SetStdHandle")
	for _, h := range []int{syscall.STD_OUTPUT_HANDLE, syscall.STD_ERROR_HANDLE} {
		_, _, _ = set.Call(uintptr(h), null.Fd())
	}
}
