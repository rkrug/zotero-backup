package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// macOS Keychain through the security tool; the entry's service is the name.

func osKeychainGet(name string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", name, "-w").Output()
	if err != nil {
		return "", errNoEntry
	}
	return strings.TrimSpace(string(out)), nil
}

func osKeychainSet(name, key string) error {
	for osKeychainDelete(name) == nil { // one entry per name
	}
	// security -i reads the command from stdin, so the key is not in the process list
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -s %s -a zotero-backup -l %s -w %s\n",
		quote(name), quote("zotero-backup: "+name), quote(key)))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() > 0 {
		return fmt.Errorf("storing in the keychain failed: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func osKeychainDelete(name string) error {
	if exec.Command("security", "delete-generic-password", "-s", name).Run() != nil {
		return errNoEntry
	}
	return nil
}

func quote(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
