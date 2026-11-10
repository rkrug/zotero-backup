package main

// find and show look into backups on disk, without the API and without a key
// (#33): find searches the backups for items, show prints one item from a
// backup and can copy its files out.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// searchText is what find matches in an item: key, title, creators, DOI, note
// and annotation text, attachment file name.
func searchText(d map[string]any) string {
	var parts []string
	for _, f := range []string{"key", "title", "shortTitle", "DOI", "filename", "annotationText", "annotationComment", "publicationTitle"} {
		if s, _ := d[f].(string); s != "" {
			parts = append(parts, s)
		}
	}
	if s, _ := d["note"].(string); s != "" {
		parts = append(parts, tags.ReplaceAllString(s, " "))
	}
	if cs, _ := d["creators"].([]any); cs != nil {
		for _, c := range cs {
			m, _ := c.(map[string]any)
			for _, f := range []string{"lastName", "firstName", "name"} {
				if s, _ := m[f].(string); s != "" {
					parts = append(parts, s)
				}
			}
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

type found struct {
	key, kind, title, parent string
	first, last              string // backup names (UTC times)
	count, of                int
	trashed, missingNewest   bool
}

// runFind searches every backup of every library below root (or of the one
// library folder given) and prints the matching items with the backups they
// are in.
func runFind(root, libFolder, text string, out io.Writer) error {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return errors.New("find needs a text to search for")
	}
	var libs []string
	if libFolder != "" {
		libs = []string{libFolder}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() && len(backups(filepath.Join(root, e.Name()))) > 0 {
				libs = append(libs, filepath.Join(root, e.Name()))
			}
		}
	}
	total := 0
	for _, lib := range libs {
		names := backups(lib)
		if len(names) == 0 {
			return fmt.Errorf("%s is not a library folder with backups", lib)
		}
		hits := map[string]*found{}
		for _, n := range names {
			var items []map[string]any
			if err := readJSON(filepath.Join(lib, n, "items.json"), &items); err != nil {
				return err
			}
			byKey := map[string]map[string]any{}
			for _, d := range items {
				k, _ := d["key"].(string)
				byKey[k] = d
			}
			for k, d := range byKey {
				h := hits[k]
				if h == nil {
					if !strings.Contains(searchText(d), text) {
						continue
					}
					h = &found{key: k, first: n}
					hits[k] = h
				}
				h.kind, _ = d["itemType"].(string)
				h.title = label(d)
				h.parent = ""
				if p, _ := d["parentItem"].(string); p != "" {
					h.parent = p + " " + label(byKey[p])
				}
				h.last, h.trashed = n, trashedWithParents(byKey, k, "parentItem")
				h.count++
			}
		}
		if len(hits) == 0 {
			continue
		}
		fmt.Fprintln(out, filepath.Base(lib))
		keys := sortedKeys(hits)
		sort.SliceStable(keys, func(i, j int) bool { return hits[keys[i]].title < hits[keys[j]].title })
		for _, k := range keys {
			h := hits[k]
			fmt.Fprintf(out, "  %s  %-16s %s\n", h.key, h.kind, h.title)
			if h.parent != "" {
				fmt.Fprintf(out, "            under %s\n", h.parent)
			}
			where := fmt.Sprintf("in %d of %d backups, %s … %s", h.count, len(names), h.first, h.last)
			switch {
			case h.last != names[len(names)-1]:
				where += "; NOT in the newest backup"
			case h.trashed:
				where += "; in the trash in the newest backup"
			}
			fmt.Fprintf(out, "            %s\n", where)
		}
		total += len(hits)
	}
	if total == 0 {
		fmt.Fprintf(out, "No item matches %q.\n", text)
	}
	return nil
}

// runShow prints item key from a backup, with its children; with saveDir it
// copies their files (ZIPs unpacked) into saveDir/<key>/.
func runShow(path, key, saveDir string, out io.Writer) error {
	b, err := loadBackup(path)
	if err != nil {
		return err
	}
	d := b.items[key]
	if d == nil {
		return fmt.Errorf("no item %s in %s (zotero-backup find shows which backups have it)", key, b.dir)
	}
	fmt.Fprintf(out, "backup   %s\nlibrary  %s\n\n", b.dir, b.label())
	printItem(out, b, d, "")
	if p, _ := d["parentItem"].(string); p != "" {
		fmt.Fprintf(out, "\nunder    %s %s\n", p, label(b.items[p]))
	}
	kids := children(b, key)
	for _, c := range kids {
		fmt.Fprintln(out)
		printItem(out, b, b.items[c], "  ")
	}
	if saveDir == "" {
		return nil
	}
	abs, _ := filepath.Abs(saveDir)
	root, _ := filepath.Abs(filepath.Dir(filepath.Dir(b.dir)))
	if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("--save %s is inside the backups (%s): choose a folder outside them", saveDir, root)
	}
	saved := 0
	for _, k := range append([]string{key}, kids...) {
		f, ok := b.files[k]
		if !ok {
			continue
		}
		dst := filepath.Join(saveDir, k)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		src := filepath.Join(b.dir, filepath.FromSlash(f.Path))
		names, err := saveFile(src, dst, safeName(f.Filename, k), f.Zip)
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Fprintf(out, "saved    %s\n", filepath.Join(dst, n))
		}
		saved++
	}
	if saved == 0 {
		fmt.Fprintln(out, "no files to save")
	}
	return nil
}

// children lists the items below key (notes, attachments, their annotations).
func children(b *backupSet, key string) []string {
	var out []string
	for _, k := range sortedKeys(b.items) {
		if p, _ := b.items[k]["parentItem"].(string); p == key {
			out = append(out, k)
			out = append(out, children(b, k)...)
		}
	}
	return out
}

func printItem(out io.Writer, b *backupSet, d map[string]any, indent string) {
	k, _ := d["key"].(string)
	t, _ := d["itemType"].(string)
	fmt.Fprintf(out, "%s%s  %s", indent, k, t)
	if trashedWithParents(b.items, k, "parentItem") {
		fmt.Fprint(out, "  (in the trash)")
	}
	fmt.Fprintln(out)
	skip := map[string]bool{"key": true, "version": true, "itemType": true, "parentItem": true, "relations": true, "deleted": true,
		"note": true, "creators": true, "tags": true, "collections": true, "dateAdded": true, "dateModified": true, "md5": true, "mtime": true,
		"annotationPosition": true, "annotationSortIndex": true}
	for _, f := range sortedKeys(d) {
		if skip[f] || isEmpty(d[f]) {
			continue
		}
		fmt.Fprintf(out, "%s  %-20s %v\n", indent, f, d[f])
	}
	if cs, _ := d["creators"].([]any); len(cs) > 0 {
		var names []string
		for _, c := range cs {
			m, _ := c.(map[string]any)
			n, _ := m["name"].(string)
			if n == "" {
				n = strings.TrimSpace(fmt.Sprint(m["lastName"], ", ", m["firstName"]))
			}
			names = append(names, n)
		}
		fmt.Fprintf(out, "%s  %-20s %s\n", indent, "creators", strings.Join(names, "; "))
	}
	if ts, _ := d["tags"].([]any); len(ts) > 0 {
		var names []string
		for _, x := range ts {
			m, _ := x.(map[string]any)
			names = append(names, fmt.Sprint(m["tag"]))
		}
		fmt.Fprintf(out, "%s  %-20s %s\n", indent, "tags", strings.Join(names, "; "))
	}
	for _, ck := range anyStrings(d["collections"]) {
		fmt.Fprintf(out, "%s  %-20s %s %s\n", indent, "collection", ck, label(b.collections[ck]))
	}
	if n, _ := d["note"].(string); n != "" {
		text := strings.Join(strings.Fields(tags.ReplaceAllString(strings.NewReplacer("</p>", "\n", "<br>", "\n").Replace(n), " ")), " ")
		fmt.Fprintf(out, "%s  %-20s %s\n", indent, "note", text)
	}
	if f, ok := b.files[k]; ok {
		fmt.Fprintf(out, "%s  %-20s %s (md5 %s, %d bytes)\n", indent, "file in the backup", f.Path, f.MD5, f.Size)
	}
}

// saveFile copies a backup file into dir as name, or unpacks it there when it
// is a ZIP (as Zotero desktop uploads text files and snapshots). It never
// overwrites a file.
func saveFile(src, dir, name string, isZip bool) ([]string, error) {
	write := func(n string, r io.Reader) error {
		n = filepath.Base(filepath.FromSlash(n)) // a ZIP entry may not leave dir
		f, err := os.OpenFile(filepath.Join(dir, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}
	if !isZip {
		in, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer in.Close()
		return []string{name}, write(name, in)
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var names []string
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return names, err
		}
		err = write(zf.Name, rc)
		rc.Close()
		if err != nil {
			return names, err
		}
		names = append(names, filepath.Base(filepath.FromSlash(zf.Name)))
	}
	return names, nil
}

// runDiffBackups compares two backups of the same library on disk (#34): what
// was added, removed or changed between them, offline. It is the restore
// comparison with the newer backup in place of the library.
func runDiffBackups(pathA, pathB string, p Profile, sel []string, out io.Writer) error {
	a, err := loadBackup(pathA)
	if err != nil {
		return err
	}
	b, err := loadBackup(pathB)
	if err != nil {
		return err
	}
	if a.meta.Library != b.meta.Library {
		return fmt.Errorf("%s and %s are backups of different libraries (%s, %s)", a.dir, b.dir, a.label(), b.label())
	}
	if filepath.Base(b.dir) < filepath.Base(a.dir) {
		a, b = b, a
	}
	newer := &libraryNow{version: b.meta.Version, items: b.items, collections: b.collections, searches: b.searches, settings: b.settings}
	pl := makePlan(a, newer, append(slices.Clone(p.Exclude), b.meta.Exclude...), sel)
	fmt.Fprintf(out, "older    %s (library version %d)\nnewer    %s (library version %d)\nlibrary  %s\n\n", a.dir, a.meta.Version, b.dir, b.meta.Version, b.label())
	var removed, changed []change
	for _, c := range pl.objects {
		if c.What == "missing" {
			removed = append(removed, c)
		} else {
			changed = append(changed, c)
		}
	}
	printSection(out, "added (only in the newer backup)", pl.extra, 0)
	printSection(out, "removed (only in the older backup)", removed, 0)
	printSection(out, "changed", changed, 0)
	var files []string
	for _, k := range pl.files {
		if b.items[k] != nil { // a removed attachment is listed above
			files = append(files, k)
		}
	}
	if len(files) > 0 {
		fmt.Fprintf(out, "files changed: %d\n", len(files))
		for _, k := range files {
			fmt.Fprintf(out, "  %s  %s\n", k, a.files[k].Filename)
		}
		fmt.Fprintln(out)
	}
	if len(pl.settings) > 0 {
		fmt.Fprintf(out, "settings changed: %s\n\n", strings.Join(pl.settings, ", "))
	}
	if len(pl.extra)+len(removed)+len(changed)+len(files)+len(pl.settings) == 0 {
		fmt.Fprintln(out, "No differences.")
	}
	return nil
}
