//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// osProcessState: signal 0 tells whether pid exists (EPERM: it does, another
// user's); ps tells its program name.
func osProcessState(pid int, program string) (alive, ours bool) {
	if pid <= 0 {
		return false, false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false, false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false, false // ps finds no such process (it ended meanwhile)
	}
	return true, sameProgram(strings.TrimSpace(string(out)), program)
}

// stopProcess asks the process to stop, as Ctrl-C would.
func stopProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
