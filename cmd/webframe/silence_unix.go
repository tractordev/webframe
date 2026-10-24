//go:build darwin || linux

package main

import "os"

// silence points the process's stdout and stderr at /dev/null, including
// what native libraries write there.
func silence() {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	_ = dup2(int(null.Fd()), 1)
	_ = dup2(int(null.Fd()), 2)
}
