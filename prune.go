package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// keep selects the backups to keep under the three-generation scheme: the
// newest backup of each of the last keepDaily days, keepWeekly ISO weeks and
// keepMonthly months that have backups. The newest backup is always kept.
// names are backup folder names, oldest first.
func keep(names []string, daily, weekly, monthly int) map[string]bool {
	kept := map[string]bool{}
	if len(names) == 0 {
		return kept
	}
	kept[names[len(names)-1]] = true
	type rule struct {
		n      int
		period func(time.Time) string
		seen   map[string]bool
	}
	rules := []*rule{
		{daily, func(t time.Time) string { return t.Format("2006-01-02") }, map[string]bool{}},
		{weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) }, map[string]bool{}},
		{monthly, func(t time.Time) string { return t.Format("2006-01") }, map[string]bool{}},
	}
	for i := len(names) - 1; i >= 0; i-- { // newest first: the first seen in a period is its newest
		t, err := time.Parse(timeLayout, names[i])
		if err != nil {
			kept[names[i]] = true // not ours to judge
			continue
		}
		for _, r := range rules {
			p := r.period(t)
			if len(r.seen) < r.n && !r.seen[p] {
				r.seen[p] = true
				kept[names[i]] = true
			}
		}
	}
	return kept
}

// prune deletes, in every library folder below root, the backups the profile
// does not keep. Files shared through hard links stay on disk as long as a kept
// backup still links to them.
func prune(root string, p Profile, dryRun bool, out io.Writer) error {
	libs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, l := range libs {
		libRoot := filepath.Join(root, l.Name())
		names := backups(libRoot)
		if len(names) == 0 {
			continue
		}
		if !dryRun { // never delete while a backup of this library runs (it may link to these files)
			release, err := acquireLock(libRoot, func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) })
			if err != nil {
				fmt.Fprintf(out, "skipped %s: %v\n", l.Name(), err)
				continue
			}
			defer release()
		}
		kept := keep(names, p.KeepDaily, p.KeepWeekly, p.KeepMonthly)
		for _, n := range names {
			if kept[n] {
				continue
			}
			if dryRun {
				fmt.Fprintf(out, "would delete %s/%s\n", l.Name(), n)
				continue
			}
			if err := os.RemoveAll(filepath.Join(libRoot, n)); err != nil {
				return err
			}
			fmt.Fprintf(out, "deleted %s/%s\n", l.Name(), n)
		}
	}
	return nil
}

// runPrune is the prune command: the backup folder must exist; the run gets a
// log of its own (not with --dry-run); after the backups, the logs are archived.
func runPrune(root string, p Profile, dryRun bool, out io.Writer) error {
	if err := checkRoot(root); err != nil {
		return err
	}
	logDir := p.logDir(root)
	if !dryRun {
		f, _, err := openRunLog(logDir, time.Now(), "prune")
		if err != nil {
			fmt.Fprintf(out, "no run log: %v\n", err)
		} else if f != nil {
			defer f.Close()
			out = io.MultiWriter(out, f)
		}
	}
	if err := prune(root, p, dryRun, out); err != nil {
		return err
	}
	return archiveLogs(root, logDir, dryRun, time.Now(), out)
}

// archiveLogs moves run logs to <logDir>/archive once they no longer belong
// to a backup there is, and gzips archived files older than a week:
//   - a backup run that made backups: when none of them exists any more;
//   - a backup run that made none (failed, stopped, all skipped, or no result
//     file): once a newer backup run succeeded (status ok or problems);
//   - prune runs: all but the newest.
//
// ponytail: a backup still running is the newest run and stays; a run started
// by hand before a newer successful one could be moved while it writes (the
// log goes on in the archive).
func archiveLogs(root, logDir string, dryRun bool, now time.Time, out io.Writer) error {
	if logDir == "" {
		return nil
	}
	entries, err := os.ReadDir(logDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	type run struct {
		files []string
		sum   *runSummary
	}
	runs := map[string]*run{} // "<start>-<command>" → its files
	var backupRuns, pruneRuns []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || len(n) <= len(timeLayout) || n[len(timeLayout)] != '-' {
			continue
		}
		if _, err := time.Parse(timeLayout, n[:len(timeLayout)]); err != nil {
			continue
		}
		id := strings.TrimSuffix(strings.TrimSuffix(n, ".log"), ".json")
		r := runs[id]
		if r == nil {
			r = &run{}
			runs[id] = r
			switch {
			case strings.HasSuffix(id, "-backup"):
				backupRuns = append(backupRuns, id)
			case strings.HasSuffix(id, "-prune"):
				pruneRuns = append(pruneRuns, id)
			}
		}
		r.files = append(r.files, n)
		if strings.HasSuffix(n, ".json") {
			s := &runSummary{}
			if readJSON(filepath.Join(logDir, n), s) == nil {
				r.sum = s
			}
		}
	}
	sort.Strings(backupRuns)
	sort.Strings(pruneRuns)
	newestGood := "" // the newest backup run that succeeded
	for _, id := range backupRuns {
		if s := runs[id].sum; s != nil && (s.Status == "ok" || s.Status == "problems") {
			newestGood = id
		}
	}
	var move []string
	for _, id := range backupRuns {
		s, made, left := runs[id].sum, 0, 0
		if s != nil {
			for _, l := range s.Libraries {
				if l.Backup != "" {
					made++
					if _, err := os.Stat(filepath.Join(root, filepath.Base(filepath.Dir(l.Backup)), filepath.Base(l.Backup))); err == nil {
						left++
					}
				}
			}
		}
		if (made > 0 && left == 0) || (made == 0 && id < newestGood) {
			move = append(move, runs[id].files...)
		}
	}
	for i, id := range pruneRuns {
		if i < len(pruneRuns)-1 {
			move = append(move, runs[id].files...)
		}
	}
	sort.Strings(move)
	archive := filepath.Join(logDir, "archive")
	for _, n := range move {
		if dryRun {
			fmt.Fprintf(out, "would archive %s\n", n)
			continue
		}
		if err := os.MkdirAll(archive, 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(logDir, n), filepath.Join(archive, n)); err != nil {
			return err
		}
	}
	if len(move) > 0 && !dryRun {
		fmt.Fprintf(out, "archived %d log files (backups gone, or a newer run succeeded) to %s\n", len(move), archive)
	}
	return gzipOld(archive, dryRun, now.Add(-7*24*time.Hour), out)
}

// gzipOld compresses the files in dir last written before cutoff (name.gz,
// same time; the original is removed once the .gz is complete).
func gzipOld(dir string, dryRun bool, cutoff time.Time, out io.Writer) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	n := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || strings.HasSuffix(e.Name(), ".gz") || !info.ModTime().Before(cutoff) {
			continue
		}
		n++
		if dryRun {
			continue
		}
		src := filepath.Join(dir, e.Name())
		if err := gzipFile(src, src+".gz"); err != nil {
			return err
		}
		os.Chtimes(src+".gz", info.ModTime(), info.ModTime())
		if err := os.Remove(src); err != nil {
			return err
		}
	}
	switch {
	case n > 0 && dryRun:
		fmt.Fprintf(out, "would compress %d archived log files older than a week\n", n)
	case n > 0:
		fmt.Fprintf(out, "compressed %d archived log files older than a week\n", n)
	}
	return nil
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	zw.Name = filepath.Base(src)
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
