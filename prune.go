package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
