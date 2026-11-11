package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLock(t *testing.T, dir string, l lockInfo) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	b, _ := json.Marshal(l)
	if err := os.WriteFile(filepath.Join(dir, lockName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func noWarn(string, ...any) {}

// a PID that is certainly not running: that of a process that has ended
func endedPID(t *testing.T) int {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

func TestLockTwiceAndRelease(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir, noWarn)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := readLock(dir)
	if l.PID != os.Getpid() || l.Host != hostName() {
		t.Errorf("lock %+v", l)
	}
	var locked *errLocked
	if _, err := acquireLock(dir, noWarn); !errors.As(err, &locked) || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second lock: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(dir, lockName)); err == nil {
		t.Error("lock left after release")
	}
	if r, err := acquireLock(dir, noWarn); err != nil {
		t.Errorf("after release: %v", err)
	} else {
		r()
	}
}

func TestLockStaleIsTakenOver(t *testing.T) {
	dir := t.TempDir()
	writeLock(t, dir, lockInfo{PID: endedPID(t), Host: hostName(), Command: "backup"})
	var warned string
	release, err := acquireLock(dir, func(f string, a ...any) { warned = f })
	if err != nil || !strings.Contains(warned, "taking over") {
		t.Fatalf("stale lock: %v, warning %q", err, warned)
	}
	release()
}

func TestLockReusedPIDIsTakenOver(t *testing.T) {
	old := processState
	processState = func(int, string) (bool, bool) { return true, false } // alive, but another program now
	t.Cleanup(func() { processState = old })
	dir := t.TempDir()
	writeLock(t, dir, lockInfo{PID: 4242, Host: hostName()})
	release, err := acquireLock(dir, noWarn)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestLockFromAnotherComputerIsNotTakenOver(t *testing.T) {
	dir := t.TempDir()
	writeLock(t, dir, lockInfo{PID: endedPID(t), Host: "elsewhere.example", Command: "backup"})
	_, err := acquireLock(dir, noWarn)
	if err == nil || !strings.Contains(err.Error(), "another computer") {
		t.Errorf("err %v", err)
	}
	if l, _ := readLock(dir); l.Host != "elsewhere.example" {
		t.Error("the other computer's lock was replaced")
	}
}

func TestReleaseKeepsAnotherRunsLock(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir, noWarn)
	if err != nil {
		t.Fatal(err)
	}
	writeLock(t, dir, lockInfo{PID: 4242, Host: hostName()}) // taken over meanwhile
	release()
	if l, err := readLock(dir); err != nil || l.PID != 4242 {
		t.Errorf("release removed another run's lock: %v %+v", err, l)
	}
}

// fakeRunning makes every lock with PID 4242 look like a running zotero-backup.
func fakeRunning(t *testing.T) {
	old := processState
	processState = func(pid int, _ string) (bool, bool) { return pid == 4242, pid == 4242 }
	t.Cleanup(func() { processState = old })
}

func TestBackupSkipsALockedLibrary(t *testing.T) {
	fakeRunning(t)
	f := sampleLibrary()
	root := t.TempDir()
	writeLock(t, filepath.Join(root, "group-1-Test_group"), lockInfo{PID: 4242, Host: hostName(), Command: "backup"})
	p := defaultProfile()
	p.Library, p.To = "group:1", root
	var out strings.Builder
	sum, err := runBackup(fixed(fakeClient(t, f)), p, &out, quietProgress())
	if err != nil || sum.Skipped != 1 || sum.Failed != 0 || sum.Status != "ok" ||
		!strings.Contains(out.String(), "1 skipped") || !strings.Contains(out.String(), "running: PID 4242") {
		t.Errorf("err %v, summary %+v\n%s", err, sum, out.String())
	}
	if len(backups(filepath.Join(root, "group-1-Test_group"))) != 0 || findPartial(filepath.Join(root, "group-1-Test_group")) != "" {
		t.Error("wrote into the locked library")
	}
}

func TestPruneSkipsALockedLibrary(t *testing.T) {
	fakeRunning(t)
	f := sampleLibrary()
	root := t.TempDir()
	runFake(t, f, root, nil, day(1))
	f.version++
	runFake(t, f, root, nil, day(2))
	lib := filepath.Join(root, "group-1-Test_group")
	writeLock(t, lib, lockInfo{PID: 4242, Host: hostName(), Command: "backup"})
	p := defaultProfile()
	p.KeepDaily, p.KeepWeekly, p.KeepMonthly = 1, 0, 0
	var out strings.Builder
	if err := prune(root, p, false, &out); err != nil {
		t.Fatal(err)
	}
	if len(backups(lib)) != 2 || !strings.Contains(out.String(), "skipped group-1-Test_group") {
		t.Errorf("pruned a locked library: %d left\n%s", len(backups(lib)), out.String())
	}
	os.Remove(filepath.Join(lib, lockName))
	out.Reset()
	if err := prune(root, p, false, &out); err != nil || len(backups(lib)) != 1 {
		t.Errorf("unlocked: %v, %d left\n%s", err, len(backups(lib)), out.String())
	}
	if _, err := os.Stat(filepath.Join(lib, lockName)); err == nil {
		t.Error("prune left its lock")
	}
}

// TestLockHelper is the "running backup" for TestStop: it takes the lock of
// ZB_LOCK_DIR, stops on a signal like a backup, and waits. It does nothing
// when run normally.
func TestLockHelper(t *testing.T) {
	dir := os.Getenv("ZB_LOCK_DIR")
	if dir == "" {
		return
	}
	if _, err := acquireLock(dir, noWarn); err != nil {
		os.Exit(2)
	}
	stopOnSignal(io.Discard)
	os.WriteFile(filepath.Join(dir, "ready"), nil, 0o644)
	time.Sleep(time.Minute)
	os.Exit(3)
}

func TestStopARunningBackup(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "group-1-Test_group")
	os.MkdirAll(lib, 0o755)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelper$")
	cmd.Env = append(os.Environ(), "ZB_LOCK_DIR="+lib)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(lib, "ready")); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var out strings.Builder
	runStatus(root, &out)
	if !strings.Contains(out.String(), "group-1-Test_group  running: PID") {
		t.Fatalf("status:\n%s", out.String())
	}
	if err := runStop(root, "user", &out); err == nil {
		t.Error("stop user: no error although no backup of user runs")
	}
	out.Reset()
	if err := runStop(root, "group-1", &out); err != nil || !strings.Contains(out.String(), "stopped PID") {
		t.Fatalf("stop: %v\n%s", err, out.String())
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the helper still runs")
	}
	if _, err := os.Stat(filepath.Join(lib, lockName)); err == nil {
		t.Error("lock left after stop")
	}
	out.Reset()
	runStatus(root, &out)
	if !strings.Contains(out.String(), "No backup is running") {
		t.Errorf("status after stop:\n%s", out.String())
	}
}

func TestStopLeavesStaleAndForeignLocks(t *testing.T) {
	root := t.TempDir()
	writeLock(t, filepath.Join(root, "group-1-A"), lockInfo{PID: endedPID(t), Host: hostName(), Command: "backup"})
	writeLock(t, filepath.Join(root, "group-2-B"), lockInfo{PID: os.Getpid(), Host: "elsewhere.example", Command: "backup"})
	var out strings.Builder
	if err := runStop(root, "", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "group-1-A  stale") || !strings.Contains(out.String(), "group-2-B  on another computer") ||
		strings.Contains(out.String(), "stopped PID") {
		t.Errorf("stop:\n%s", out.String())
	}
}

func TestSameProgram(t *testing.T) {
	cases := []struct {
		shown, program string
		want           bool
	}{
		{"/Users/x/bin/zotero-backup", "zotero-backup", true},
		{"/tmp/zbl", "zbl", true},                        // renamed: still recognised
		{"/usr/bin/python3", "zotero-backup", false},     // the PID now runs something else
		{"zotero-backup.t", "zotero-backup.test", true},  // Linux: 15 characters
		{"ZOTERO-BACKUP.EXE", "zotero-backup.exe", true}, // Windows: any case
		{"/Users/x/bin/zotero-backup", "", true},         // a lock without a program name
		{"/tmp/zbl", "", false},
	}
	for _, c := range cases {
		if got := sameProgram(c.shown, c.program); got != c.want {
			t.Errorf("sameProgram(%q, %q) = %v", c.shown, c.program, got)
		}
	}
}

func TestLockRecordsTheProgram(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir, noWarn)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if l, _ := readLock(dir); l.Program != progName() || l.Program == "" {
		t.Errorf("program %q, want %q", l.Program, progName())
	}
	if alive, ours := osProcessState(os.Getpid(), progName()); !alive || !ours {
		t.Errorf("this process: alive %v, ours %v", alive, ours)
	}
}
