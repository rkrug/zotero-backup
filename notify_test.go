package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNotifyHelper is the notify command in the tests below: it writes its
// stdin and environment to ZB_HELPER_OUT. It does nothing when run normally.
func TestNotifyHelper(t *testing.T) {
	path := os.Getenv("ZB_HELPER_OUT")
	if path == "" {
		return
	}
	in, _ := io.ReadAll(os.Stdin)
	os.WriteFile(path, []byte(string(in)+"\n--env--\n"+strings.Join(os.Environ(), "\n")), 0o644)
	if os.Getenv("ZB_HELPER_FAIL") != "" {
		os.Exit(3)
	}
}

func helperProfile(t *testing.T) (Profile, string) {
	out := filepath.Join(t.TempDir(), "notified")
	t.Setenv("ZB_HELPER_OUT", out)
	p := defaultProfile()
	p.NotifyCommand = []string{os.Args[0], "-test.run=^TestNotifyHelper$"}
	return p, out
}

func TestNotifyOnFailedBackup(t *testing.T) {
	p, out := helperProfile(t)
	t.Setenv("ZOTERO_API_KEY", "secret-key-value")
	f := sampleLibrary()
	c := fakeClient(t, f)
	p.Library, p.To = "group:1", t.TempDir()
	os.WriteFile(filepath.Join(p.To, "group-1-Test_group"), []byte("a file where the library folder goes"), 0o644)
	var stdout, stderr strings.Builder
	err := backupAndNotify(fixed(c), p, &stdout, &stderr, quietProgress())
	if err == nil {
		t.Fatal("backup did not fail")
	}
	b, rerr := os.ReadFile(out)
	got := string(b)
	if rerr != nil || !strings.Contains(got, "FAILED") || !strings.Contains(got, "ZB_STATUS=failed") ||
		!strings.Contains(got, `ZB_LIBRARIES_FAILED=group-1 "Test group"`) {
		t.Errorf("notification: %v\n%s", rerr, got)
	}
	if strings.Contains(got, "secret-key-value") {
		t.Error("the API key reached the notify command")
	}
}

func TestNoNotifyWhenAllWell(t *testing.T) {
	p, out := helperProfile(t)
	p.Library, p.To = "group:1", t.TempDir()
	if err := backupAndNotify(fixed(fakeClient(t, sampleLibrary())), p, io.Discard, io.Discard, quietProgress()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("notified although the backup went well")
	}
}

func TestNotifyFailureKeepsBackupResult(t *testing.T) {
	p, _ := helperProfile(t)
	t.Setenv("ZB_HELPER_FAIL", "1")
	errs := notify(p, "failed", "summary", nil)
	if len(errs) != 1 {
		t.Fatalf("errors %v", errs)
	}
	if err := notifyTest(defaultProfile(), io.Discard); err == nil || !strings.Contains(err.Error(), "no notify_command") {
		t.Errorf("notify-test without settings: %v", err)
	}
	t.Setenv("ZB_HELPER_FAIL", "")
	var out strings.Builder
	if err := notifyTest(p, &out); err != nil || !strings.Contains(out.String(), "sent") {
		t.Errorf("notify-test: %v", err)
	}
}
