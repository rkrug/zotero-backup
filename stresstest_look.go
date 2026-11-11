package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lookChecks (part of the extras), on real backups: diff of two backups shows
// exactly the change the test made, find finds the fixture items, show lists a
// family with its children and --save copies its PDF; and a second backup of
// the library is refused while its lock is held. b0 is the backup before the
// changes; fams have their title changed and synced since.
func (s *stress) lookChecks(b0 *backupSet, fams []family) {
	const sc = "extras"
	b1, err := s.backup()
	if err != nil {
		s.record(sc, "look", "backup after the changes", false, "%v", err)
		return
	}

	var out strings.Builder
	err = runDiffBackups(b0.dir, b1.dir, s.profile, nil, &out)
	ok := err == nil && !strings.Contains(out.String(), "removed (only in the older backup)")
	for _, f := range fams {
		ok = ok && strings.Contains(out.String(), f.Parent) && strings.Contains(out.String(), "[title]")
	}
	s.record(sc, "diff-backups", "diff of two backups shows the titles changed by the test, nothing removed", ok, "%v\n%s", err, out.String())

	out.Reset()
	name := "[zb-e2e] " + fams[0].Run + " " + stressOps[0].name // the fixtures' title (and their notes)
	err = runFind(s.root, filepath.Dir(b1.dir), name, &out)
	ok = err == nil
	for _, f := range fams {
		ok = ok && strings.Contains(out.String(), f.Parent)
	}
	s.record(sc, "find", "find finds the fixture items by title in the backups", ok, "%v\n%s", err, out.String())

	f := fams[0]
	out.Reset()
	save := filepath.Join(os.TempDir(), "zb-e2e-save-"+s.run)
	os.RemoveAll(save)
	defer os.RemoveAll(save)
	err = runShow(b0.dir, f.Parent, save, &out)
	ok = err == nil
	for _, k := range []string{f.Parent, f.Note, f.PDF, f.Annotation, f.File} {
		ok = ok && (k == "" || strings.Contains(out.String(), k))
	}
	saved, _ := os.ReadDir(filepath.Join(save, f.PDF))
	s.record(sc, "show", "show lists the family with its note, attachments and annotation; --save copies the PDF", ok && len(saved) == 1,
		"%v, saved %d\n%s", err, len(saved), out.String())

	lib := filepath.Dir(b1.dir)
	release, err := acquireLock(lib, s.pr.Warnf)
	if err != nil {
		s.record(sc, "lock", "take the library's lock", false, "%v", err)
		return
	}
	_, _, err = backupLibrary(s.c, s.lib, s.root, nil, time.Now(), quietProgress())
	var locked *errLocked
	release()
	_, lerr := os.Stat(filepath.Join(lib, lockName))
	s.record(sc, "lock", "a second backup is refused while a backup holds the library's lock; the lock goes with it", errors.As(err, &locked) && lerr != nil, "%v", err)
}
