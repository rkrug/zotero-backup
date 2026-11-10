package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Profile holds the settings of one named profile. Fields missing from a
// profile in the config file keep the values of defaultProfile.
type Profile struct {
	Library     string            `json:"library"`      // "all", "user" or "group:<id>"
	Key         string            `json:"key"`          // keychain entry name of the default key, never the key itself
	Keys        map[string]string `json:"keys"`         // per library ("user", "group:<id>", or "default"): keychain entry name
	WriteKey    string            `json:"write_key"`    // keychain entry restore uses for every library ("" = the library's key)
	To          string            `json:"to"`           // backup directory
	Exclude     []string          `json:"exclude"`      // parts left out of backup and restore
	KeepDaily   int               `json:"keep_daily"`   // prune: newest backup of each of the last N days
	KeepWeekly  int               `json:"keep_weekly"`  // prune: newest backup of each of the last N weeks
	KeepMonthly int               `json:"keep_monthly"` // prune: newest backup of each of the last N months

	Progress         string `json:"progress"`          // auto, line, log or quiet
	ProgressInterval int    `json:"progress_interval"` // seconds between progress lines in log mode

	NotifyCommand []string `json:"notify_command"` // run after a backup that failed or had problems: program and arguments, no shell
	NotifyMacOS   bool     `json:"notify_macos"`   // and a macOS notification
}

type Config struct {
	Profiles map[string]Profile `json:"profiles"`
}

// excludable lists the values allowed in Profile.Exclude.
var excludable = []string{"files", "attachments", "notes", "annotations", "fulltext", "settings", "trash"}

func defaultProfile() Profile {
	return Profile{
		Library:     "all",
		Key:         "zotero-backup",
		Keys:        map[string]string{},
		WriteKey:    "",
		To:          "~/zotero-backups",
		Exclude:     []string{},
		KeepDaily:   7,
		KeepWeekly:  5,
		KeepMonthly: 12,

		Progress:         "auto",
		ProgressInterval: 30,

		NotifyCommand: []string{},
	}
}

func defaultConfig() Config {
	return Config{Profiles: map[string]Profile{"default": defaultProfile()}}
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "zotero-backup.json"
	}
	return filepath.Join(dir, "zotero-backup", "config.json")
}

// loadConfig reads the config file; a missing file gives the default config.
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (Config, error) {
	var raw struct {
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg := defaultConfig()
	for name, msg := range raw.Profiles {
		p := defaultProfile()
		dec := json.NewDecoder(bytes.NewReader(msg))
		dec.DisallowUnknownFields() // a misspelt option is an error, not silently ignored
		if err := dec.Decode(&p); err != nil {
			return Config{}, fmt.Errorf("config: profile %q: %w", name, err)
		}
		if err := p.validate(); err != nil {
			return Config{}, fmt.Errorf("config: profile %q: %w", name, err)
		}
		cfg.Profiles[name] = p
	}
	return cfg, nil
}

func (p Profile) validate() error {
	if _, _, err := parseLibrary(p.Library); err != nil {
		return err
	}
	for lib, name := range p.Keys {
		if lib != "default" {
			if kind, _, err := parseLibrary(lib); err != nil || kind == "all" {
				return fmt.Errorf("keys: %q is not \"default\", \"user\" or \"group:<id>\"", lib)
			}
		}
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("keys: no key name for %q", lib)
		}
	}
	for _, e := range p.Exclude {
		if !slices.Contains(excludable, e) {
			return fmt.Errorf("exclude: unknown value %q (allowed: %s)", e, strings.Join(excludable, ", "))
		}
	}
	if !slices.Contains([]string{"auto", "line", "log", "quiet"}, p.Progress) {
		return fmt.Errorf("progress: %q is not one of auto, line, log, quiet", p.Progress)
	}
	if p.ProgressInterval < 1 {
		return errors.New("progress_interval must be at least 1 (seconds)")
	}
	if len(p.NotifyCommand) > 0 && strings.TrimSpace(p.NotifyCommand[0]) == "" {
		return errors.New("notify_command: the first entry must be the program")
	}
	if p.KeepDaily < 0 || p.KeepWeekly < 0 || p.KeepMonthly < 0 {
		return errors.New("keep_daily, keep_weekly and keep_monthly must not be negative")
	}
	return nil
}

// parseLibrary splits "user", "group:<id>" or "all" into kind and group ID.
func parseLibrary(s string) (kind string, groupID int, err error) {
	switch {
	case s == "all" || s == "user":
		return s, 0, nil
	case strings.HasPrefix(s, "group:"):
		id, err := strconv.Atoi(strings.TrimPrefix(s, "group:"))
		if err != nil || id <= 0 {
			return "", 0, fmt.Errorf("library %q: group ID must be a positive number", s)
		}
		return "group", id, nil
	}
	return "", 0, fmt.Errorf("library %q: use \"all\", \"user\" or \"group:<id>\"", s)
}

// keyName is the keychain entry to use for a library ("user", "group:<id>"):
// its entry in "keys", else keys["default"], else "key".
func (p Profile) keyName(lib string) string {
	if n := p.Keys[lib]; n != "" {
		return n
	}
	if n := p.Keys["default"]; n != "" {
		return n
	}
	return p.Key
}

// keyNames lists the profile's distinct key names, each with what it is for.
func (p Profile) keyNames() map[string][]string {
	out := map[string][]string{p.keyName("default"): {"default"}}
	for lib, name := range p.Keys {
		if lib != "default" {
			out[name] = append(out[name], lib)
		}
	}
	if p.WriteKey != "" {
		out[p.WriteKey] = append(out[p.WriteKey], "restore")
	}
	return out
}

// forRestore returns the profile as restore uses it: with write_key set, that
// key for every library.
func (p Profile) forRestore() Profile {
	if p.WriteKey != "" {
		p.Key, p.Keys = p.WriteKey, map[string]string{}
	}
	return p
}
