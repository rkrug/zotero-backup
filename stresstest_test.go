package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStressDialog(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "Yes\n": true, "\n": false, "n\n": false, "yes please\n": false, "": false} {
		var out strings.Builder
		if got := askStress(bufio.NewReader(strings.NewReader(in)), &out, `"Safe Edit Test" (group 6719916)`); got != want {
			t.Errorf("answer %q: %v, want %v", in, got, want)
		}
		for _, w := range []string{"This test is altering the content of the group or library.", "DO ONLY RUN IT ON A TEST GROUP OR LIBRARY!",
			`Are you sure that "Safe Edit Test" (group 6719916) is a test group or library`, "[N/y]"} {
			if !strings.Contains(out.String(), w) {
				t.Errorf("dialog lacks %q:\n%s", w, out.String())
			}
		}
	}
}

func TestStressDialogNeedsTerminal(t *testing.T) {
	r, w, _ := os.Pipe() // a piped "y" must not get past the question
	w.WriteString("y\n")
	w.Close()
	defer r.Close()
	if ok, err := confirmStress(r, io.Discard, "x"); ok || err == nil {
		t.Errorf("piped answer accepted: %v %v", ok, err)
	}
}

func TestStressRefusals(t *testing.T) {
	p := defaultProfile()
	for _, c := range []struct {
		o    stressOptions
		want string
	}{
		{stressOptions{}, "at least one --library"},
		{stressOptions{libraries: []string{"group:1"}, scenarios: []string{"nope"}}, `unknown scenario "nope"`},
		{stressOptions{libraries: []string{"group:1"}, ops: []string{"nope"}}, `unknown operation "nope"`},
	} {
		err := runStresstest(p, c.o, os.Stdin, io.Discard, quietProgress())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err %v, want %q", c.o, err, c.want)
		}
	}
	if _, err := stressCheck(p, "all", ""); err == nil || !strings.Contains(err.Error(), `"all" is not accepted`) {
		t.Errorf("all: %v", err)
	}
}

func TestZoteroProfiles(t *testing.T) {
	ini := "[General]\nStartWithLastProfile=0\n\n[Profile1]\nName=default\nIsRelative=1\nPath=Profiles/a.default\n\n" +
		"[Profile2]\nName=TEST - Fixes\nIsRelative=1\nPath=Profiles/b.IPBES - PAND\nDefault=1\n\n[Profile3]\nName=Old\nIsRelative=0\nPath=/elsewhere/Old\n"
	for want, path := range map[string]string{"": "/Z/Profiles/b.IPBES - PAND", "default": "/Z/Profiles/a.default", "Old": "/elsewhere/Old"} {
		got, err := parseProfilesINI(ini, "/Z", want)
		if err != nil || filepath.ToSlash(got) != path {
			t.Errorf("profile %q: %q %v", want, got, err)
		}
	}
	if _, err := parseProfilesINI(ini, "/Z", "missing"); err == nil {
		t.Error("missing profile not reported")
	}
}

func TestStressReports(t *testing.T) {
	dir := t.TempDir()
	r := &stressReport{Run: "20261109T120000", Tool: "v0.1.0", Started: time.Now()}
	r.Libraries = []stressLibrary{{"group-6719916", "Safe Edit Test", "/b/x"}}
	r.add(stressResult{"group-6719916", "api", "edit-title", "server = backup", "pass", "", time.Now()})
	r.add(stressResult{"group-6719916", "api", "erase-item", "desktop = server", "fail", "item ABC <b>fields</b> [title]", time.Now()})
	r.add(stressResult{"group-6719916", "unsynced-local", "*", "conflicts", "info", "2", time.Now()})
	if err := r.write(dir); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Summary   stressSummary        `json:"summary"`
		Results   []stressResult       `json:"results"`
		Scenarios []stressScenarioInfo `json:"scenarios"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Summary != (stressSummary{Checks: 2, Passed: 1, Failed: 1, Info: 1}) || len(back.Results) != 3 || len(back.Scenarios) != len(stressScenarios) {
		t.Errorf("json: %+v", back)
	}
	h, _ := os.ReadFile(filepath.Join(dir, "report.html"))
	html := string(h)
	for _, w := range []string{"Failed checks", "erase-item", "&lt;b&gt;fields&lt;/b&gt;", "Safe Edit Test", "unsynced-local", "✘ fail", "ℹ info"} {
		if !strings.Contains(html, w) {
			t.Errorf("html lacks %q", w)
		}
	}
	if strings.Contains(html, "<b>fields</b>") {
		t.Error("detail not escaped in the HTML report")
	}
}
