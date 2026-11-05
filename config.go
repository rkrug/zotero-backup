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
	Library     string   `json:"library"`      // "all", "user" or "group:<id>"
	Key         string   `json:"key"`          // keychain entry name, never the key itself
	To          string   `json:"to"`           // backup directory
	Exclude     []string `json:"exclude"`      // parts left out of backup and restore
	KeepDaily   int      `json:"keep_daily"`   // prune: daily snapshots to keep
	KeepMonthly int      `json:"keep_monthly"` // prune: monthly snapshots to keep
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
		To:          "~/zotero-backups",
		Exclude:     []string{},
		KeepDaily:   30,
		KeepMonthly: 12,
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
	for _, e := range p.Exclude {
		if !slices.Contains(excludable, e) {
			return fmt.Errorf("exclude: unknown value %q (allowed: %s)", e, strings.Join(excludable, ", "))
		}
	}
	if p.KeepDaily < 0 || p.KeepMonthly < 0 {
		return errors.New("keep_daily and keep_monthly must not be negative")
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
