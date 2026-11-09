package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStressDialog(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "Yes\n": true, "\n": false, "n\n": false, "yes please\n": false, "": false} {
		var out strings.Builder
		if got := askStress(bufio.NewReader(strings.NewReader(in)), &out, `the group "Safe Edit Test" (ID 6719916)`); got != want {
			t.Errorf("answer %q: %v, want %v", in, got, want)
		}
		for _, w := range []string{"This test is altering the content of the group or library.", "DO ONLY RUN IT ON A TEST GROUP OR LIBRARY!",
			`Are you sure that the group "Safe Edit Test" (ID 6719916) is a test group or library`, "[N/y]"} {
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

func TestStressHelp(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"stresstest"}, &out); err != nil { // no options: the help, nothing runs
		t.Fatal(err)
	}
	for _, w := range []string{"Steps:", "Zotero Debug Bridge", "zotero-backup key set zb-test-write",
		"zotero-backup stresstest --key zb-test-write\n", "--scenarios desktop-synced", "report.json",
		"ONLY on test groups or libraries", "asks for each one by name", defaultConfigPath()} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("help lacks %q", w)
		}
	}
	for _, sc := range stressScenarios {
		if !strings.Contains(stressUsage(), sc.name) {
			t.Errorf("help does not list scenario %s", sc.name)
		}
	}
	for _, o := range stressOps {
		if !strings.Contains(stressUsage(), o.name) {
			t.Errorf("help does not list operation %s", o.name)
		}
	}
}

func TestStressRefusals(t *testing.T) {
	p := defaultProfile()
	for _, c := range []struct {
		o    stressOptions
		want string
	}{
		{stressOptions{scenarios: []string{"nope"}}, `unknown scenario "nope"`}, // before any key is needed
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
	r.add(stressResult{"group-6719916", "Safe Edit Test", "api", "edit-title", "server = backup", "pass", "", time.Now()})
	r.add(stressResult{"group-6719916", "Safe Edit Test", "api", "erase-item", "desktop = server", "fail", "item ABC <b>fields</b> [title]", time.Now()})
	r.add(stressResult{"group-6719916", "Safe Edit Test", "unsynced-local", "*", "conflicts", "info", "2", time.Now()})
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

// scopeServer: user 7 is in groups 1, 2 and 3. Key "narrow" writes only to
// group 1; "wide" also to the personal library and group 2; "all" to every group.
func scopeServer(t *testing.T) func(string) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys/current":
			switch r.Header.Get("Zotero-API-Key") {
			case "narrow":
				fmt.Fprint(w, `{"userID":7,"username":"tester","access":{"user":{"library":true},"groups":{"all":{"library":true},"1":{"library":true,"write":true}}}}`)
			case "wide":
				fmt.Fprint(w, `{"userID":7,"username":"tester","access":{"user":{"library":true,"write":true},"groups":{"1":{"library":true,"write":true},"2":{"library":true,"write":true}}}}`)
			case "all":
				fmt.Fprint(w, `{"userID":7,"username":"tester","access":{"user":{"library":true},"groups":{"all":{"library":true,"write":true}}}}`)
			}
		case "/users/7/groups":
			w.Header().Set("Total-Results", "3")
			fmt.Fprint(w, `[{"id":1,"data":{"name":"Test group"}},{"id":2,"data":{"name":"Real work"}},{"id":3,"data":{"name":"Other"}}]`)
		}
	}))
	t.Cleanup(srv.Close)
	return func(key string) *Client { return &Client{Base: srv.URL, Key: key, HTTP: srv.Client()} }
}

func TestStressKeyScope(t *testing.T) {
	c := scopeServer(t)
	if err := checkKeyScope(c("narrow"), map[string]bool{"group-1": true}); err != nil {
		t.Errorf("narrow key refused: %v", err)
	}
	err := checkKeyScope(c("wide"), map[string]bool{"group-1": true})
	if err == nil || !strings.Contains(err.Error(), `user-7 "tester", group-2 "Real work"`) || !strings.Contains(err.Error(), "separate Zotero account") {
		t.Errorf("wide key: %v", err)
	}
	if err := checkKeyScope(c("wide"), map[string]bool{"group-1": true, "group-2": true, "user-7": true}); err != nil {
		t.Errorf("wide key with all its libraries named: %v", err)
	}
	if err := checkKeyScope(c("all"), map[string]bool{"group-1": true}); err == nil || !strings.Contains(err.Error(), `group-3 "Other"`) {
		t.Errorf("key writing to all groups: %v", err)
	}
}

func TestBridgeRequest(t *testing.T) {
	var got struct{ ct, auth, query, body string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.ct, got.auth, got.query, got.body = r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.URL.RawQuery, string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`"10.0.8"`))
	}))
	defer srv.Close()
	t.Setenv("ZOTERO_DEBUG_BRIDGE_TOKEN", "tok")
	t.Setenv("ZOTERO_DEBUG_BRIDGE_URL", srv.URL)
	b, err := newBridge(t.TempDir(), "group", 6719916, "Safe Edit Test")
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := b.run("return Zotero.version", nil, &v); err != nil || v != "10.0.8" {
		t.Fatalf("%q %v", v, err)
	}
	if got.ct != "text/plain" || got.auth != "Bearer tok" || !strings.Contains(got.query, "id=6719916") ||
		!strings.Contains(got.query, "name=Safe+Edit+Test") || !strings.HasSuffix(got.body, "return Zotero.version") ||
		!strings.Contains(got.body, "const TAG = 'zb-e2e'") {
		t.Errorf("request %+v", got)
	}
}

func TestParentTrashed(t *testing.T) {
	cols := map[string]map[string]any{
		"TOP": {"deleted": true}, "MID": {"parentCollection": "TOP"}, "LOW": {"parentCollection": "MID"},
		"OTHER": {"parentCollection": false},
	}
	for k, want := range map[string]bool{"TOP": false, "MID": true, "LOW": true, "OTHER": false, "NONE": false} {
		if got := parentTrashed(cols, k); got != want {
			t.Errorf("%s: %v, want %v", k, got, want)
		}
	}
}

func TestStressCleanupNeedsARun(t *testing.T) {
	// a wrong run would erase nothing or, empty, every run's objects: refused before anything else
	err := runStresstest(defaultProfile(), stressOptions{cleanup: "yesterday"}, os.Stdin, io.Discard, quietProgress())
	if err == nil || !strings.Contains(err.Error(), "--cleanup") {
		t.Fatalf("got %v", err)
	}
	for it, want := range map[string]string{"book": "extra", "note": "note", "attachment": "title"} {
		if got := touchedField(map[string]any{"itemType": it}); got != want {
			t.Errorf("touchedField(%s) = %s, want %s", it, got, want)
		}
	}
}
