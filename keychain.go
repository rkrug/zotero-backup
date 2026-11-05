package main

// API keys in the system keychain: macOS Keychain, Windows Credential
// Manager, Linux Secret Service. Entries are found by name (the profile's
// "key"); the key itself is never printed, logged or passed as an argument.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var errNoEntry = errors.New("no such keychain entry")

// The keychain operations; variables so tests can replace them.
var (
	keychainGet    = osKeychainGet
	keychainSet    = osKeychainSet
	keychainDelete = osKeychainDelete
)

// apiKey returns ZOTERO_API_KEY, or else the keychain entry named by the profile
// for its default key.
func apiKey(p Profile) (string, error) { return apiKeyNamed(p.keyName("default")) }

// apiKeyNamed returns ZOTERO_API_KEY (which wins for every library), or else the
// keychain entry name.
func apiKeyNamed(name string) (string, error) {
	if k := os.Getenv("ZOTERO_API_KEY"); k != "" {
		return k, nil
	}
	k, err := keychainGet(name)
	if errors.Is(err, errNoEntry) {
		return "", fmt.Errorf("no API key: ZOTERO_API_KEY is not set and there is no keychain entry %q (zotero-backup key set %s)", name, name)
	}
	if err != nil {
		return "", fmt.Errorf("no API key: ZOTERO_API_KEY is not set and the keychain could not be read: %w", err)
	}
	return k, nil
}

// readSecret reads one line: without echo on a terminal, as is from a pipe.
func readSecret(prompt string, in *os.File, out io.Writer) (string, error) {
	if st, err := in.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(out, prompt)
		restore, err := echoOff(in)
		if err == nil {
			defer func() { restore(); fmt.Fprintln(out) }()
		}
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", errors.New("no key given")
	}
	return strings.TrimSpace(line), nil
}

// keySet asks for a key, checks it with Zotero and stores it under name.
func keySet(name string, newClientFor func(string) *Client, in *os.File, out io.Writer) error {
	key, err := readSecret(fmt.Sprintf("API key for %q (input hidden): ", name), in, out)
	if err != nil {
		return err
	}
	k, err := newClientFor(key).keyInfo()
	if err != nil {
		return fmt.Errorf("Zotero does not accept this key: %w", err)
	}
	if err := keychainSet(name, key); err != nil {
		return err
	}
	fmt.Fprintf(out, "stored as %q: key of %s (%d); zotero-backup key check %s shows its rights\n", name, k.Username, k.UserID, name)
	return nil
}

func keyRemove(name string, out io.Writer) error {
	if err := keychainDelete(name); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %q from the keychain (the key itself stays valid at zotero.org/settings/keys)\n", name)
	return nil
}

// clientsFor returns a function giving the client for a library ("default",
// "user", "group:<id>"), with the key the profile names for it. Clients are
// shared between libraries that use the same key.
func clientsFor(p Profile, newClientFor func(string) *Client) func(lib string) (*Client, error) {
	clients := map[string]*Client{}
	return func(lib string) (*Client, error) {
		name := p.keyName(lib)
		if c := clients[name]; c != nil {
			return c, nil
		}
		key, err := apiKeyNamed(name)
		if err != nil {
			return nil, err
		}
		clients[name] = newClientFor(key)
		return clients[name], nil
	}
}
