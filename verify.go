package main

// verify checks backups on disk, without network access (#23): the JSON files
// parse (and match their checksums in meta.json, for backups that have them),
// the counts match meta.json, every file is there with its size and md5, and
// every attachment with a file is either backed up or listed as missing.

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// verifier checks backups; files hard-linked between backups are read once.
type verifier struct {
	quick  bool                     // sizes only, no md5
	seen   map[string][]os.FileInfo // md5 → files already read and found right
	hashed int                      // files read for their md5 (tests)
}

type verifyResult struct {
	dir      string
	files    int
	bytes    int64
	problems []string
	notes    []string
	old      bool // made before JSON checksums
}

// runVerify is the verify command. target is a backup folder, a library
// folder (its newest backup), "all" (every backup below root), or "" (the
// newest backup of each library below root).
func runVerify(root, target string, quick bool, out io.Writer) error {
	var dirs []string
	switch target {
	case "", "all":
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			lib := filepath.Join(root, e.Name())
			names := backups(lib)
			if len(names) == 0 {
				continue
			}
			if target == "" {
				names = names[len(names)-1:]
			}
			for _, n := range names {
				dirs = append(dirs, filepath.Join(lib, n))
			}
		}
		if len(dirs) == 0 {
			return fmt.Errorf("no backups in %s", root)
		}
	default:
		if _, err := os.Stat(filepath.Join(target, "meta.json")); err == nil {
			dirs = []string{target}
		} else if latest := latestBackup(target); latest != "" {
			dirs = []string{latest}
		} else {
			return fmt.Errorf("%s is neither a backup nor a library folder with backups", target)
		}
	}
	v := &verifier{quick: quick, seen: map[string][]os.FileInfo{}}
	bad, old := 0, 0
	for _, d := range dirs {
		r := v.check(d)
		label := filepath.Join(filepath.Base(filepath.Dir(d)), filepath.Base(d))
		what := fmt.Sprintf("%d files, %.1f MB", r.files, float64(r.bytes)/1e6)
		if quick {
			what += ", sizes only"
		}
		if len(r.problems) == 0 {
			fmt.Fprintf(out, "OK        %s (%s)\n", label, what)
		} else {
			bad++
			fmt.Fprintf(out, "PROBLEMS  %s (%s): %d\n", label, what, len(r.problems))
			for _, p := range r.problems {
				fmt.Fprintf(out, "  %s\n", p)
			}
		}
		for _, n := range r.notes {
			fmt.Fprintf(out, "  note: %s\n", n)
		}
		if r.old {
			old++
		}
	}
	if old > 0 {
		fmt.Fprintf(out, "%d of %d backups were made before v0.3.1: their JSON files are checked for being readable and their counts, not for changes.\n", old, len(dirs))
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d backups have problems", bad, len(dirs))
	}
	fmt.Fprintf(out, "%d backups checked, no problems.\n", len(dirs))
	return nil
}

func (v *verifier) check(dir string) verifyResult {
	r := verifyResult{dir: dir}
	bad := func(f string, a ...any) { r.problems = append(r.problems, fmt.Sprintf(f, a...)) }

	var m Meta
	if err := readJSON(filepath.Join(dir, "meta.json"), &m); err != nil {
		bad("meta.json: %v", err)
		return r
	}
	for name, want := range m.Checksums {
		if sum, _, err := md5File(filepath.Join(dir, name)); err != nil {
			bad("%s: %v", name, err)
		} else if sum != want {
			bad("%s: changed since the backup (md5 %s, recorded %s)", name, sum, want)
		}
	}
	r.old = m.Checksums == nil

	var items, collections, searches []map[string]any
	var settings, fulltext map[string]json.RawMessage
	var files map[string]FileEntry
	for name, dst := range map[string]any{"items.json": &items, "collections.json": &collections, "searches.json": &searches,
		"settings.json": &settings, "fulltext.json": &fulltext, "files.json": &files} {
		if err := readJSON(filepath.Join(dir, name), dst); err != nil {
			bad("%s: %v", name, err)
		}
	}
	if len(r.problems) > 0 && (items == nil || files == nil) {
		return r
	}

	got := map[string]int{"collections": len(collections), "searches": len(searches), "fulltext": len(fulltext), "files": len(files)}
	byKey := map[string]map[string]any{}
	for _, d := range items {
		t, _ := d["itemType"].(string)
		got[t]++
		k, _ := d["key"].(string)
		byKey[k] = d
	}
	var kinds []string
	for k := range m.Counts {
		kinds = append(kinds, k)
	}
	for k := range got {
		if _, ok := m.Counts[k]; !ok && got[k] > 0 {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		if m.Counts[k] != got[k] {
			bad("%s: %d in the files, %d in meta.json", k, got[k], m.Counts[k])
		}
	}

	if !slices.Contains(m.Exclude, "files") && !slices.Contains(m.Exclude, "attachments") {
		missing := map[string]bool{}
		for _, mf := range m.Missing {
			missing[mf.Key] = true
		}
		for _, k := range sortedKeys(byKey) {
			d := byKey[k]
			lm, _ := d["linkMode"].(string)
			sum, _ := d["md5"].(string)
			if d["itemType"] != "attachment" || (lm != "imported_file" && lm != "imported_url") || sum == "" {
				continue
			}
			if _, ok := files[k]; !ok && !missing[k] {
				bad("attachment %s (%s): file neither in the backup nor listed as missing", k, label(d))
			}
		}
	}

	for _, k := range sortedKeys(files) {
		e := files[k]
		path := filepath.Join(dir, filepath.FromSlash(e.Path))
		info, err := os.Stat(path)
		if err != nil {
			bad("file %s %s: missing from the backup (%v)", k, e.Path, err)
			continue
		}
		r.files++
		r.bytes += info.Size()
		if !e.Zip && info.Size() != e.Size {
			bad("file %s %s: %d bytes, recorded %d", k, e.Path, info.Size(), e.Size)
			continue
		}
		if v.quick || v.known(e.MD5, info) {
			continue
		}
		sum, err := v.sum(path, e)
		switch {
		case err != nil:
			bad("file %s %s: %v", k, e.Path, err)
		case sum != e.MD5:
			bad("file %s %s: md5 %s, recorded %s (the file changed or is damaged)", k, e.Path, sum, e.MD5)
		default:
			v.seen[e.MD5] = append(v.seen[e.MD5], info)
		}
	}

	if p := findPartial(filepath.Dir(dir)); p != "" {
		r.notes = append(r.notes, filepath.Base(p)+" in the library folder: an unfinished backup, continued by the next run")
	}
	return r
}

// known: the same file (a hard link) was read and found right before.
func (v *verifier) known(sum string, info os.FileInfo) bool {
	for _, s := range v.seen[sum] {
		if os.SameFile(s, info) {
			return true
		}
	}
	return false
}

// sum is the md5 a file entry records: the file's, or for a ZIP the one of
// the attachment's file inside it.
func (v *verifier) sum(path string, e FileEntry) (string, error) {
	v.hashed++
	if e.Zip {
		return zippedMD5(path, e.Filename)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
