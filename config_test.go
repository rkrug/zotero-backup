package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingConfigGivesDefaults(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["default"]
	if p.Library != "all" || p.KeepDaily != 30 || p.KeepMonthly != 12 || len(p.Exclude) != 0 {
		t.Errorf("default profile = %+v", p)
	}
}

func TestProfileKeepsDefaultsForMissingFields(t *testing.T) {
	cfg, err := parseConfig([]byte(`{"profiles":{"test":{"library":"group:6719916","exclude":["fulltext"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["test"]
	if p.Library != "group:6719916" || p.Exclude[0] != "fulltext" {
		t.Errorf("set fields not taken: %+v", p)
	}
	if p.Key != "zotero-backup" || p.KeepDaily != 30 || p.To != "~/zotero-backups" {
		t.Errorf("missing fields did not keep defaults: %+v", p)
	}
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Error("default profile missing when the file does not define it")
	}
}

func TestConfigErrors(t *testing.T) {
	for name, tc := range map[string]struct{ json, want string }{
		"misspelt option":  {`{"profiles":{"p":{"exlude":["files"]}}}`, "unknown field"},
		"unknown exclude":  {`{"profiles":{"p":{"exclude":["pdfs"]}}}`, `unknown value "pdfs"`},
		"bad library":      {`{"profiles":{"p":{"library":"group:abc"}}}`, "positive number"},
		"bad library kind": {`{"profiles":{"p":{"library":"users"}}}`, `use "all"`},
		"negative keep":    {`{"profiles":{"p":{"keep_daily":-1}}}`, "negative"},
		"unknown top key":  {`{"profile":{}}`, "unknown field"},
	} {
		_, err := parseConfig([]byte(tc.json))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
