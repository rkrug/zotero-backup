package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lastRun(t *testing.T, root string) *runSummary {
	t.Helper()
	s := &runSummary{}
	if err := readJSON(filepath.Join(root, lastRunName), s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSummaryAfterSuccessAndNewMissingFile(t *testing.T) {
	f := sampleLibrary()
	p := defaultProfile()
	p.Library, p.To = "group:1", t.TempDir()
	var out strings.Builder
	sum, err := runBackup(fixed(fakeClient(t, f)), p, &out, quietProgress())
	if err != nil || sum.Status != "ok" || sum.NewMissing != 0 {
		t.Fatalf("first run: %v %+v", err, sum)
	}
	last := strings.TrimSpace(out.String()[strings.LastIndex(strings.TrimSpace(out.String()), "\n")+1:])
	if !strings.HasPrefix(last, "backup finished: 1 libraries (1 backed up, 0 failed, 0 skipped), 0 problems, 1 missing files (0 new)") {
		t.Errorf("summary line: %q", last)
	}
	if s := lastRun(t, p.To); s.Status != "ok" || s.ExitStatus != 0 || len(s.Libraries) != 1 || s.Libraries[0].Result != "backed up" || s.Libraries[0].Missing != 1 {
		t.Errorf("last-run.json: %+v", s)
	}

	time.Sleep(1100 * time.Millisecond) // the next backup gets its own folder (names to the second)
	f.addFile("NOFILE02", "BOOK0001", "gone.pdf", "", false)
	f.version++
	out.Reset()
	sum, err = runBackup(fixed(fakeClient(t, f)), p, &out, quietProgress())
	if err != nil || sum.Status != "ok" || sum.NewMissing != 1 || sum.Missing != 2 {
		t.Fatalf("second run: %v %+v", err, sum)
	}
	if !strings.Contains(out.String(), "2 missing files (1 new)") {
		t.Errorf("output:\n%s", out.String())
	}
	if s := lastRun(t, p.To); len(s.Libraries[0].NewMissing) != 1 || s.Libraries[0].NewMissing[0].Key != "NOFILE02" {
		t.Errorf("new missing: %+v", s.Libraries[0].NewMissing)
	}
}

func TestSummaryAfterFailedLibraryQuiet(t *testing.T) {
	p := defaultProfile()
	p.Library, p.To = "group:1", t.TempDir()
	os.WriteFile(filepath.Join(p.To, "group-1-Test_group"), []byte("a file where the library folder goes"), 0o644)
	var out strings.Builder
	sum, err := runBackup(fixed(fakeClient(t, sampleLibrary())), p, &out, quietProgress())
	if err == nil || sum.Status != "failed" || sum.ExitStatus != 1 || sum.Failed != 1 {
		t.Fatalf("%v %+v", err, sum)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "FAILED") ||
		!strings.HasPrefix(lines[1], "backup failed: 1 libraries (0 backed up, 1 failed, 0 skipped)") ||
		!strings.Contains(lines[1], `; failed: group-1 "Test group"`) {
		t.Errorf("quiet output (only failures and the summary):\n%s", out.String())
	}
	if s := lastRun(t, p.To); s.Status != "failed" || s.Libraries[0].Reason == "" {
		t.Errorf("last-run.json: %+v", s)
	}
}

func TestSummaryWhenNoLibraryCanBeRead(t *testing.T) {
	p := defaultProfile()
	p.Library, p.To = "group:999", t.TempDir()
	var out strings.Builder
	_, err := runBackup(fixed(fakeClient(t, sampleLibrary())), p, &out, quietProgress())
	if err == nil {
		t.Fatal("no error")
	}
	s := lastRun(t, p.To)
	if s.Status != "failed" || s.Error == "" || !strings.HasPrefix(out.String(), "backup failed: ") {
		t.Errorf("%+v\n%s", s, out.String())
	}
}

func TestSummaryAfterStop(t *testing.T) {
	s := &runSummary{Started: time.Now().UTC()}
	s.add(libResult{Library: "group-1", Name: "A", Result: "backed up", Downloaded: 3, DownBytes: 2e9})
	s.add(libResult{Library: "group-9 (C)", Result: "no access", Reason: "no access"})
	s.working(libResult{Library: "group-2", Name: "B"})
	s.finish(nil, true)
	if got := s.line(); !strings.HasPrefix(got, "backup stopped: 2 libraries (1 backed up, 0 failed, 0 skipped), 1 without access, ") ||
		!strings.Contains(got, "3 files downloaded (2.0 GB)") || !strings.HasSuffix(got, `; stopped: group-2 "B"`) || s.ExitStatus != 1 {
		t.Errorf("%q, exit %d", got, s.ExitStatus)
	}
	root := t.TempDir()
	if err := s.write(root); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printLastRun(root, &out)
	if !strings.HasPrefix(out.String(), "last run  ") || !strings.Contains(out.String(), "exit status 1: backup stopped: 2 libraries") {
		t.Errorf("schedule show: %q", out.String())
	}
}
