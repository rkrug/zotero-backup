package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Linux: the freedesktop Secret Service (GNOME Keyring, KWallet, KeePassXC)
// through secret-tool; the secret goes in on stdin.

var errNoSecretTool = errors.New("no keychain: install secret-tool (package libsecret-tools / libsecret) with a running Secret Service, or set ZOTERO_API_KEY")

func secretTool(stdin string, args ...string) (string, error) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return "", errNoSecretTool
	}
	cmd := exec.Command("secret-tool", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("secret-tool: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), err
}

func osKeychainGet(name string) (string, error) {
	out, err := secretTool("", "lookup", "service", name, "account", "zotero-backup")
	if errors.Is(err, errNoSecretTool) {
		return "", err
	}
	if err != nil || out == "" {
		return "", errNoEntry
	}
	return out, nil
}

func osKeychainSet(name, key string) error {
	_, err := secretTool(key, "store", "--label=zotero-backup: "+name, "service", name, "account", "zotero-backup")
	return err
}

func osKeychainDelete(name string) error {
	if _, err := osKeychainGet(name); err != nil {
		return err
	}
	_, err := secretTool("", "clear", "service", name, "account", "zotero-backup")
	return err
}
