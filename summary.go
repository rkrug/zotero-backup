package main

// The result of a backup run (#24): one summary line at the end (also when
// libraries failed, and when the run is stopped) and <to>/last-run.json for
// monitoring, with the same facts per library. Never contains an API key.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const lastRunName = "last-run.json"

type libResult struct {
	Library    string        `json:"library"`
	Name       string        `json:"name,omitempty"`
	Result     string        `json:"result"`           // backed up, failed, skipped, stopped, no access
	Reason     string        `json:"reason,omitempty"` // why failed / skipped
	Backup     string        `json:"backup,omitempty"`
	Version    int           `json:"version,omitempty"`
	Items      int           `json:"items"`
	Files      int           `json:"files"`
	Downloaded int           `json:"files_downloaded"`
	DownBytes  int64         `json:"bytes_downloaded"`
	Linked     int           `json:"files_linked"`
	Missing    int           `json:"missing_files"`
	NewMissing []MissingFile `json:"new_missing_files,omitempty"` // not missing in the previous backup
	Problems   []string      `json:"problems,omitempty"`
	Seconds    float64       `json:"seconds"`
}

func (r libResult) label() string { return libraryLabel(r.Library, r.Name) }

type runSummary struct {
	mu         sync.Mutex
	Started    time.Time   `json:"started"`
	Finished   time.Time   `json:"finished"`
	Seconds    float64     `json:"seconds"`
	Status     string      `json:"status"` // ok, problems, failed, stopped
	ExitStatus int         `json:"exit_status"`
	Error      string      `json:"error,omitempty"`
	BackedUp   int         `json:"backed_up"`
	Failed     int         `json:"failed"`
	Skipped    int         `json:"skipped"`
	NoAccess   int         `json:"no_access"` // libraries the key cannot read (library "all"); not counted as libraries of the run
	Problems   int         `json:"problems"`
	Missing    int         `json:"missing_files"`
	NewMissing int         `json:"new_missing_files"`
	Downloaded int         `json:"files_downloaded"`
	DownBytes  int64       `json:"bytes_downloaded"`
	Linked     int         `json:"files_linked"`
	Libraries  []libResult `json:"libraries"`
	current    *libResult  // the library being backed up, for a stop
}

func (s *runSummary) add(r libResult) {
	s.mu.Lock()
	s.Libraries = append(s.Libraries, r)
	s.current = nil
	s.mu.Unlock()
}

func (s *runSummary) working(r libResult) {
	s.mu.Lock()
	s.current = &r
	s.mu.Unlock()
}

// finish adds up the libraries and sets the status; err is the run's error.
func (s *runSummary) finish(err error, stopped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stopped && s.current != nil {
		r := *s.current
		r.Result = "stopped"
		s.Libraries = append(s.Libraries, r)
		s.current = nil
	}
	s.Finished = time.Now().UTC()
	s.Seconds = s.Finished.Sub(s.Started).Seconds()
	s.BackedUp, s.Failed, s.Skipped, s.NoAccess, s.Problems, s.Missing, s.NewMissing, s.Downloaded, s.DownBytes, s.Linked = 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
	for _, r := range s.Libraries {
		switch r.Result {
		case "backed up":
			s.BackedUp++
		case "failed":
			s.Failed++
		case "skipped":
			s.Skipped++
		case "no access":
			s.NoAccess++
		}
		s.Problems += len(r.Problems)
		s.Missing += r.Missing
		s.NewMissing += len(r.NewMissing)
		s.Downloaded += r.Downloaded
		s.DownBytes += r.DownBytes
		s.Linked += r.Linked
	}
	s.Status, s.ExitStatus = "ok", 0
	switch {
	case stopped:
		s.Status, s.ExitStatus = "stopped", 1
	case err != nil:
		s.Status, s.ExitStatus, s.Error = "failed", 1, err.Error()
	case s.Problems > 0: // new missing files are reported, not a problem: often a file still being uploaded
		s.Status = "problems"
	}
}

// line is the summary line: what the run did, in one line.
func (s *runSummary) line() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	word := map[string]string{"ok": "finished", "problems": "finished", "failed": "failed", "stopped": "stopped"}[s.Status]
	if len(s.Libraries) == 0 && s.Error != "" {
		return fmt.Sprintf("backup failed: %s", s.Error)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "backup %s: %d libraries (%d backed up, %d failed, %d skipped)", word, len(s.Libraries)-s.NoAccess, s.BackedUp, s.Failed, s.Skipped)
	if s.NoAccess > 0 {
		fmt.Fprintf(&b, ", %d without access", s.NoAccess)
	}
	fmt.Fprintf(&b, ", %d problems, %d missing files (%d new), %d files downloaded (%s), %d linked, %s",
		s.Problems, s.Missing, s.NewMissing, s.Downloaded, fmtBytes(s.DownBytes), s.Linked,
		time.Duration(s.Seconds*float64(time.Second)).Round(time.Second))
	for _, what := range []string{"failed", "skipped", "stopped"} {
		var names []string
		for _, r := range s.Libraries {
			if r.Result == what {
				n := r.label()
				if what == "skipped" && r.Reason != "" {
					n += " (" + r.Reason + ")"
				}
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "; %s: %s", what, strings.Join(names, ", "))
		}
	}
	return b.String()
}

func fmtBytes(n int64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

// libLine is a library's line in the output and the notification.
func libLine(r libResult) string {
	switch r.Result {
	case "backed up":
		return fmt.Sprintf("%s: %s  version %d, %d items, %d files (%d downloaded, %.1f MB; %d linked), %d missing (%d new), %d problems, %.0f s",
			r.label(), r.Backup, r.Version, r.Items, r.Files, r.Downloaded, float64(r.DownBytes)/1e6, r.Linked, r.Missing, len(r.NewMissing), len(r.Problems), r.Seconds)
	case "skipped", "no access":
		return fmt.Sprintf("%s: skipped: %s", r.label(), r.Reason)
	}
	return fmt.Sprintf("%s: FAILED: %s", r.label(), r.Reason)
}

// write saves the summary as root/last-run.json (replacing the previous one
// in one step). A backup folder that cannot be written to is reported, not fatal.
func (s *runSummary) write(root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(root); err != nil {
		return err
	}
	tmp := filepath.Join(root, lastRunName+".tmp")
	if err := writeJSON(tmp, s); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, lastRunName))
}

// newMissing are the files missing in m that were not missing in the previous
// backup of the library (none without a previous backup: nothing to compare).
func newMissing(dir string, m Meta) []MissingFile {
	if m.Previous == "" {
		return nil
	}
	var prev Meta
	if readJSON(filepath.Join(filepath.Dir(dir), m.Previous, "meta.json"), &prev) != nil {
		return nil
	}
	was := map[string]bool{}
	for _, f := range prev.Missing {
		was[f.Key] = true
	}
	var out []MissingFile
	for _, f := range m.Missing {
		if !was[f.Key] {
			out = append(out, f)
		}
	}
	return out
}

// lockedHere: the library is being backed up by another run on this computer.
func lockedHere(err error) (*errLocked, bool) {
	var l *errLocked
	return l, errors.As(err, &l) && l.info.Host == hostName()
}

var (
	stopMu   sync.Mutex
	stopHook func() // run on a stop signal before exiting
)

func onStop(f func()) {
	stopMu.Lock()
	stopHook = f
	stopMu.Unlock()
}

func runStopHook() {
	stopMu.Lock()
	f := stopHook
	stopMu.Unlock()
	if f != nil {
		f()
	}
}

// printLastRun prints a line about the last run recorded below root (schedule show).
func printLastRun(root string, out io.Writer) {
	var s runSummary
	if readJSON(filepath.Join(root, lastRunName), &s) != nil {
		return
	}
	fmt.Fprintf(out, "last run  %s, exit status %d: %s\n", s.Finished.Local().Format("2006-01-02 15:04"), s.ExitStatus, s.line())
}
