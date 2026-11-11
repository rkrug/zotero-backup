package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunChecksOnTheFake runs the stress test's run checks against the fake
// server and a stand-in for the Debug Bridge (it answers every script with {}),
// so that a fault shows here and not an hour into a real run. The stop check
// needs the real program and is left out.
func TestRunChecksOnTheFake(t *testing.T) {
	old := stressSelf
	stressSelf = func() (string, error) { return "", errors.New("not in unit tests") }
	t.Cleanup(func() { stressSelf = old })
	desktop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	}))
	t.Cleanup(desktop.Close)

	c := fakeClient(t, sampleLibrary())
	c.Key = "secret-key-value-123"
	libs, _, err := resolveLibraries(c, "group:1")
	if err != nil {
		t.Fatal(err)
	}
	rep := &stressReport{}
	p := defaultProfile()
	s := &stress{pr: quietProgress(), run: "20261111T120000", c: c, lib: libs[0], api: serverOps{c, libs[0].Path},
		z: &bridge{endpoint: desktop.URL, http: desktop.Client(), kind: "group", id: 1}, root: t.TempDir(), profile: p, rep: rep}
	s.runChecks()

	passed := 0
	for _, r := range rep.Results {
		t.Logf("%s: %s", r.Result, r.Check)
		switch {
		case r.Result == "pass":
			passed++
		case r.Check == "stop: prepare":
		case r.Result == "info":
			t.Logf("info: %s: %s", r.Check, r.Detail)
		default:
			t.Errorf("%s: %s\n%s", r.Result, r.Check, r.Detail)
		}
	}
	want := 11 // 10 run checks + verify; the notification only where there is /bin/sh
	if runtime.GOOS == "windows" {
		want--
	}
	if passed < want {
		t.Errorf("%d checks passed, want at least %d", passed, want)
	}
	if _, err := os.Stat(filepath.Join(s.root, "runs", s.lib.ID, "logs", "archive")); err != nil {
		t.Error("nothing archived")
	}
	for _, r := range rep.Results {
		if strings.Contains(r.Detail, c.Key) {
			t.Errorf("the key in the report: %s", r.Check)
		}
	}
}
