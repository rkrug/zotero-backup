package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupRefusesAMissingBackupFolder(t *testing.T) {
	p := defaultProfile()
	p.Library, p.To = "group:1", filepath.Join(t.TempDir(), "not-mounted", "zotero-backups")
	var out strings.Builder
	sum, err := runBackup(fixed(fakeClient(t, sampleLibrary())), p, &out, quietProgress())
	if err == nil || !strings.Contains(err.Error(), "does not exist") || sum.Status != "failed" {
		t.Fatalf("err %v, status %s", err, sum.Status)
	}
	if _, err := os.Stat(filepath.Dir(p.To)); err == nil {
		t.Error("the missing backup folder (or its parent) was created")
	}
	if err := runPrune(p.To, p, false, io.Discard); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("prune: %v", err)
	}
}

func TestBackupWritesARunLog(t *testing.T) {
	p := defaultProfile()
	p.Library, p.To = "group:1", t.TempDir()
	sum, err := runBackup(fixed(fakeClient(t, sampleLibrary())), p, io.Discard, quietProgress())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(p.To, "logs", runLogName(sum.Started, "backup", ".log"))
	if sum.Log != want {
		t.Fatalf("log %q, want %q", sum.Log, want)
	}
	b, _ := os.ReadFile(want)
	log := string(b)
	for _, s := range []string{"1/1 group-1-Test_group: items", `group-1 "Test group": `, "backup finished: 1 libraries"} {
		if !strings.Contains(log, s) {
			t.Errorf("run log lacks %q:\n%s", s, log)
		}
	}
	if s := lastRun(t, p.To); s.Log != want {
		t.Errorf("last-run.json log %q", s.Log)
	}
	if _, err := os.Stat(filepath.Join(p.To, "logs", runLogName(sum.Started, "backup", ".json"))); err != nil {
		t.Errorf("no result file next to the log: %v", err)
	}

	p.LogDir = ""
	p.To = t.TempDir()
	if sum, _ := runBackup(fixed(fakeClient(t, sampleLibrary())), p, io.Discard, quietProgress()); sum.Log != "" {
		t.Errorf("log_dir \"\": a log %q", sum.Log)
	}
	if _, err := os.Stat(filepath.Join(p.To, "logs")); err == nil {
		t.Error(`log_dir "": a logs folder`)
	}
}

// writeRun makes a backup run's log and result file in logDir.
func writeRun(t *testing.T, logDir string, start time.Time, status string, backups ...string) {
	t.Helper()
	s := &runSummary{Started: start, Status: status}
	for _, b := range backups {
		s.Libraries = append(s.Libraries, libResult{Result: "backed up", Backup: b})
	}
	os.MkdirAll(logDir, 0o755)
	os.WriteFile(filepath.Join(logDir, runLogName(start, "backup", ".log")), []byte("log\n"), 0o644)
	if err := writeJSON(filepath.Join(logDir, runLogName(start, "backup", ".json")), s); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveLogs(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	kept := filepath.Join(root, "group-1-A", "2026-11-03T020000Z")
	os.MkdirAll(kept, 0o755)
	gone := "/elsewhere/zotero-backups/group-1-A/2026-11-01T020000Z" // pruned (and the backup folder since moved: matched by its last two parts)
	writeRun(t, logs, day(1), "ok", gone)
	writeRun(t, logs, day(2), "failed")                                                 // no backup, a newer run succeeded
	writeRun(t, logs, day(3), "ok", "/x/group-1-A/2026-11-03T020000Z")                  // its backup is still there
	writeRun(t, logs, day(4), "failed")                                                 // no backup, nothing newer succeeded
	os.WriteFile(filepath.Join(logs, runLogName(day(5), "backup", ".log")), nil, 0o644) // running: no result file yet
	for _, d := range []int{1, 2, 3} {
		os.WriteFile(filepath.Join(logs, runLogName(day(d), "prune", ".log")), nil, 0o644)
	}
	os.WriteFile(filepath.Join(logs, "notes.txt"), nil, 0o644) // not ours

	var out strings.Builder
	if err := archiveLogs(root, logs, true, day(10), &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "would archive"); n != 6 {
		t.Errorf("dry run: %d to archive\n%s", n, out.String())
	}
	if _, err := os.Stat(filepath.Join(logs, "archive")); err == nil {
		t.Error("dry run moved files")
	}

	if err := archiveLogs(root, logs, false, day(10), io.Discard); err != nil {
		t.Fatal(err)
	}
	names := func(dir string) string {
		es, _ := os.ReadDir(dir)
		var n []string
		for _, e := range es {
			if !e.IsDir() {
				n = append(n, e.Name())
			}
		}
		return strings.Join(n, " ")
	}
	wantLeft := "2026-11-03T060000Z-backup.json 2026-11-03T060000Z-backup.log 2026-11-03T060000Z-prune.log 2026-11-04T060000Z-backup.json 2026-11-04T060000Z-backup.log 2026-11-05T060000Z-backup.log notes.txt"
	if got := names(logs); got != wantLeft {
		t.Errorf("left in logs:\n%s\nwant\n%s", got, wantLeft)
	}
	wantArchived := "2026-11-01T060000Z-backup.json 2026-11-01T060000Z-backup.log 2026-11-01T060000Z-prune.log 2026-11-02T060000Z-backup.json 2026-11-02T060000Z-backup.log 2026-11-02T060000Z-prune.log"
	if got := names(filepath.Join(logs, "archive")); got != wantArchived {
		t.Errorf("archived:\n%s\nwant\n%s", got, wantArchived)
	}
}

func TestArchivedLogsAreCompressedAfterAWeek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "archive")
	os.MkdirAll(dir, 0o755)
	old, recent := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	os.WriteFile(old, []byte("old log\n"), 0o644)
	os.WriteFile(recent, []byte("new log\n"), 0o644)
	eight := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(old, eight, eight)
	if err := gzipOld(dir, false, time.Now().Add(-7*24*time.Hour), io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the old log is still uncompressed")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("the recent log was compressed")
	}
	f, err := os.Open(old + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if string(b) != "old log\n" {
		t.Errorf("content %q", b)
	}
	if info, _ := os.Stat(old + ".gz"); !info.ModTime().Equal(eight) && info.ModTime().Sub(eight).Abs() > time.Second {
		t.Errorf("time %v, want %v", info.ModTime(), eight)
	}
}

func TestScheduleWithRunLogsIsQuiet(t *testing.T) {
	s := newScheduler("darwin", t.TempDir(), "/x/zotero-backup", "default", "", io.Discard)
	s.logDir = "/b/logs"
	for _, content := range s.files(2, 30) {
		if !strings.Contains(content, "'backup' '--quiet'") || strings.Contains(content, "--progress") {
			t.Errorf("job:\n%s", content)
		}
	}
}
