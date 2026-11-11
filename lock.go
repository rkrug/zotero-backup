package main

// A lock per library folder (#43): a backup or prune of a library holds
// <library folder>/.lock while it works there, so two runs (one by hand, one
// from the schedule) never write into the same folder. The lock holds the
// process ID, so "zotero-backup stop" can stop the run cleanly.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const lockName = ".lock"

type lockInfo struct {
	PID     int       `json:"pid"`
	Host    string    `json:"host"`
	Program string    `json:"program"` // the running program's file name, e.g. zotero-backup
	Since   time.Time `json:"since"`
	Command string    `json:"command"`
}

func (l lockInfo) String() string {
	return fmt.Sprintf("PID %d on %s, since %s (%s)", l.PID, l.Host, l.Since.Local().Format("2006-01-02 15:04"), l.Command)
}

// errLocked: another run holds the library's lock.
type errLocked struct {
	dir  string
	info lockInfo
}

func (e *errLocked) Error() string {
	return fmt.Sprintf("a backup of %s is already running (%s): skipped", filepath.Base(e.dir), e.info)
}

// processState tells whether pid is alive and whether it runs program, the
// program that took the lock (PIDs are reused). Replaced in tests.
var processState = osProcessState

// progName is this program's file name (whatever it was renamed to).
func progName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return filepath.Base(os.Args[0])
}

// sameProgram compares a process's name as the system shows it with the
// program recorded in a lock: Linux shows at most 15 characters, Windows any
// case; locks without a program name count zotero-backup.
func sameProgram(shown, program string) bool {
	shown = filepath.Base(shown)
	if program == "" {
		return strings.Contains(strings.ToLower(shown), "zotero-backup")
	}
	program = filepath.Base(program)
	return strings.EqualFold(shown, program) || (len(shown) == 15 && strings.HasPrefix(program, shown))
}

func hostName() string {
	h, _ := os.Hostname()
	return h
}

func readLock(dir string) (lockInfo, error) {
	var l lockInfo
	b, err := os.ReadFile(filepath.Join(dir, lockName))
	if err == nil {
		err = json.Unmarshal(b, &l)
	}
	return l, err
}

var (
	heldMu sync.Mutex
	held   = map[string]bool{} // lock files this process holds; removed on a stop signal
)

// acquireLock takes the lock of library folder dir for this process. A lock
// left by a process that no longer runs on this computer (or is no longer
// zotero-backup) is taken over with a warning; a lock from another computer
// is never taken over. release removes the lock if it is still this one.
func acquireLock(dir string, warn func(string, ...any)) (release func(), err error) {
	path := filepath.Join(dir, lockName)
	me := lockInfo{PID: os.Getpid(), Host: hostName(), Program: progName(), Since: time.Now().UTC(), Command: strings.Join(os.Args[1:], " ")}
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			err = json.NewEncoder(f).Encode(me)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				os.Remove(path)
				return nil, err
			}
			heldMu.Lock()
			held[path] = true
			heldMu.Unlock()
			return func() {
				heldMu.Lock()
				delete(held, path)
				heldMu.Unlock()
				if l, err := readLock(dir); err == nil && l.PID == me.PID && l.Host == me.Host {
					os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		other, rerr := readLock(dir)
		if rerr != nil { // half-written by a run that died at once
			if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) > time.Minute {
				warn("removing an unreadable lock in %s", filepath.Base(dir))
				os.Remove(path)
				continue
			}
			return nil, fmt.Errorf("%s: lock file being written by another run: try again", filepath.Base(dir))
		}
		if other.Host != me.Host {
			return nil, fmt.Errorf("%w; it is from another computer: if no backup runs there, remove %s", &errLocked{dir, other}, path)
		}
		if alive, ours := processState(other.PID, other.Program); alive && ours {
			return nil, &errLocked{dir, other}
		}
		warn("taking over the lock of %s left by a run that no longer runs (%s)", filepath.Base(dir), other)
		os.Remove(path)
	}
	return nil, fmt.Errorf("%s: could not take the lock", filepath.Base(dir))
}

// releaseHeld removes every lock this process holds (on a stop signal).
func releaseHeld() {
	heldMu.Lock()
	defer heldMu.Unlock()
	for path := range held {
		os.Remove(path)
		delete(held, path)
	}
}

// stopOnSignal makes Ctrl-C and "zotero-backup stop" end a run cleanly: the
// locks are removed, a .partial folder stays for the next run, exit status 1.
func stopOnSignal(errOut io.Writer) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		releaseHeld()
		fmt.Fprintln(errOut, "\nzotero-backup: stopped (an unfinished backup is continued by the next run)")
		os.Exit(1)
	}()
}

type runningLock struct {
	dir  string
	info lockInfo
}

// locksBelow lists the library folders below root that hold a lock.
func locksBelow(root string) []runningLock {
	entries, _ := os.ReadDir(root)
	var out []runningLock
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if l, err := readLock(dir); e.IsDir() && err == nil {
			out = append(out, runningLock{dir, l})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// lockState describes a lock: running here, stale, or on another computer.
func lockState(l lockInfo) string {
	switch alive, ours := processState(l.PID, l.Program); {
	case l.Host != hostName():
		return "on another computer"
	case alive && ours:
		return "running"
	default:
		return "stale (no such zotero-backup process; the next run takes it over)"
	}
}

// runStatus is the status command: the runs in progress below root.
func runStatus(root string, out io.Writer) error {
	locks := locksBelow(root)
	if len(locks) == 0 {
		fmt.Fprintf(out, "No backup is running in %s.\n", root)
		return nil
	}
	for _, r := range locks {
		fmt.Fprintf(out, "%s  %s: %s\n", filepath.Base(r.dir), lockState(r.info), r.info)
	}
	return nil
}

// runStop is the stop command: it stops the runs on this computer that hold a
// lock below root (only the one of library folder lib, when given), waits
// until each lock is gone, and reports.
func runStop(root, lib string, out io.Writer) error {
	locks := locksBelow(root)
	found := 0
	for _, r := range locks {
		if lib != "" && !strings.HasPrefix(filepath.Base(r.dir), lib+"-") && filepath.Base(r.dir) != lib {
			continue
		}
		found++
		state := lockState(r.info)
		if state != "running" {
			fmt.Fprintf(out, "%s  %s: %s, not stopped\n", filepath.Base(r.dir), state, r.info)
			continue
		}
		if err := stopProcess(r.info.PID); err != nil {
			return fmt.Errorf("%s: stopping PID %d: %w", filepath.Base(r.dir), r.info.PID, err)
		}
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			if alive, _ := processState(r.info.PID, r.info.Program); !alive {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if alive, _ := processState(r.info.PID, r.info.Program); alive {
			return fmt.Errorf("%s: PID %d still runs after a minute", filepath.Base(r.dir), r.info.PID)
		}
		if l, err := readLock(r.dir); err == nil && l.PID == r.info.PID { // stopped hard (Windows): its lock stayed
			os.Remove(filepath.Join(r.dir, lockName))
		}
		fmt.Fprintf(out, "%s  stopped PID %d (%s)\n", filepath.Base(r.dir), r.info.PID, r.info.Command)
	}
	switch {
	case found == 0 && lib != "":
		return fmt.Errorf("no backup of %s is running in %s", lib, root)
	case found == 0:
		fmt.Fprintf(out, "No backup is running in %s.\n", root)
	}
	return nil
}
