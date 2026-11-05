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
	if p.Library != "all" || p.KeepDaily != 7 || p.KeepWeekly != 5 || p.KeepMonthly != 12 || len(p.Exclude) != 0 {
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
	if p.Key != "zotero-backup" || p.KeepDaily != 7 || p.To != "~/zotero-backups" {
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
		"negative keep":    {`{"profiles":{"p":{"keep_weekly":-1}}}`, "negative"},
		"bad progress":     {`{"profiles":{"p":{"progress":"verbose"}}}`, "not one of"},
		"zero interval":    {`{"profiles":{"p":{"progress_interval":0}}}`, "at least 1"},
		"keys for all":     {`{"profiles":{"p":{"keys":{"all":"x"}}}}`, "keys:"},
		"keys bad lib":     {`{"profiles":{"p":{"keys":{"groups:1":"x"}}}}`, "keys:"},
		"keys no name":     {`{"profiles":{"p":{"keys":{"group:1":" "}}}}`, "no key name"},
		"unknown top key":  {`{"profile":{}}`, "unknown field"},
	} {
		_, err := parseConfig([]byte(tc.json))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestKeyNamePerLibrary(t *testing.T) {
	cfg, err := parseConfig([]byte(`{"profiles":{"p":{"key":"readonly","keys":{"group:6719916":"safe-edit-write"}},
		"q":{"key":"ignored","keys":{"default":"dflt","user":"mine"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p, q := cfg.Profiles["p"], cfg.Profiles["q"]
	for _, c := range []struct{ got, want string }{
		{p.keyName("group:6719916"), "safe-edit-write"},
		{p.keyName("group:1"), "readonly"},
		{p.keyName("default"), "readonly"},
		{q.keyName("group:1"), "dflt"},
		{q.keyName("user"), "mine"},
	} {
		if c.got != c.want {
			t.Errorf("key name %q, want %q", c.got, c.want)
		}
	}
	if n := p.keyNames(); len(n) != 2 || n["safe-edit-write"][0] != "group:6719916" || n["readonly"][0] != "default" {
		t.Errorf("keyNames %v", n)
	}
}
