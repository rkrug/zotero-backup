//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// osProcessState asks tasklist for the process and its image name.
func osProcessState(pid int, program string) (alive, ours bool) {
	if pid <= 0 {
		return false, false
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil || !strings.Contains(string(out), fmt.Sprintf(`"%d"`, pid)) {
		return false, false
	}
	image, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(string(out)), `"`), `"`) // "zotero-backup.exe","1234",…
	return true, sameProgram(image, program)
}

// stopProcess ends the process: Windows has no polite stop for another
// console process, so its lock may stay; runStop removes it afterwards.
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
