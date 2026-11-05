// Command zotero-backup backs up and restores Zotero libraries through the
// Zotero web API, keeping the item keys. See Issue #1 for the plan.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const usage = `usage: zotero-backup <command> [options]

commands:
  key check   show the user, libraries and permissions of the API key
  config      print the effective configuration (all options with their values)

options:
  --config PATH    configuration file (default: %s)
  --profile NAME   profile to use (default: default)

The API key is read from ZOTERO_API_KEY, or else from the keychain entry named
by the profile's "key" (macOS only for now).
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "zotero-backup:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("zotero-backup", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "configuration file")
	profileName := fs.String("profile", "default", "profile to use")
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, defaultConfigPath()) }

	if len(args) == 0 {
		fs.Usage()
		return errors.New("no command given")
	}
	cmd, rest := args[0], args[1:]
	if cmd == "key" && len(rest) > 0 {
		cmd, rest = "key "+rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[*profileName]
	if !ok {
		return fmt.Errorf("no profile %q in %s", *profileName, *cfgPath)
	}

	switch cmd {
	case "config":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg)
	case "key check":
		key, err := apiKey(profile)
		if err != nil {
			return err
		}
		return keyCheck(newClient(key), profile, out)
	case "help", "-h", "--help":
		fs.Usage()
		return nil
	}
	fs.Usage()
	return fmt.Errorf("unknown command %q", strings.Join(args[:min(len(args), 2)], " "))
}

// apiKey returns ZOTERO_API_KEY, or the keychain entry named by the profile.
func apiKey(p Profile) (string, error) {
	if k := os.Getenv("ZOTERO_API_KEY"); k != "" {
		return k, nil
	}
	// ponytail: macOS keychain via the security tool; Linux / Windows keychains when needed
	if runtime.GOOS != "darwin" {
		return "", errors.New("set ZOTERO_API_KEY (keychain support is macOS-only for now)")
	}
	b, err := exec.Command("security", "find-generic-password", "-s", p.Key, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no API key: ZOTERO_API_KEY is not set and keychain entry %q was not found", p.Key)
	}
	return strings.TrimSpace(string(b)), nil
}

// keyCheck prints who the key belongs to and what it may do in each library,
// marking the libraries the profile covers. It never prints the key.
func keyCheck(c *Client, p Profile, out io.Writer) error {
	k, err := c.keyInfo()
	if err != nil {
		return err
	}
	groups, err := c.groups(k.UserID)
	if err != nil {
		return err
	}
	kind, gid, _ := parseLibrary(p.Library)
	mark := func(isUser bool, id int) string {
		if kind == "all" || (kind == "user" && isUser) || (kind == "group" && !isUser && id == gid) {
			return "*"
		}
		return " "
	}
	fmt.Fprintf(out, "user %s (%d)\n\n", k.Username, k.UserID)
	fmt.Fprintf(out, "%s user          %s\n", mark(true, 0), rights(k.Access.User, true))
	found := kind != "group"
	for _, g := range groups {
		fmt.Fprintf(out, "%s group:%-7d %s  %s\n", mark(false, g.ID), g.ID, rights(k.GroupAccess(g.ID), false), g.Data.Name)
		found = found || g.ID == gid
	}
	fmt.Fprintf(out, "\n* = covered by profile library %q\n", p.Library)
	if !found {
		return fmt.Errorf("profile library %q: the key's user is not a member of this group", p.Library)
	}
	return nil
}

func rights(a Access, user bool) string {
	if !a.Library {
		return "no access"
	}
	s := "read"
	if a.Write {
		s = "read/write"
	}
	if user {
		if a.Notes {
			s += ", notes"
		}
		if a.Files {
			s += ", files"
		}
	}
	return s
}
