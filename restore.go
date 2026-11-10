package main

// diff and restore: compare a backup with the library today, and make the
// library match the backup again, keeping all keys.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// backupSet is one backup read from disk.
type backupSet struct {
	dir         string
	meta        Meta
	libPath     string
	items       map[string]map[string]any
	collections map[string]map[string]any
	searches    map[string]map[string]any
	settings    map[string]json.RawMessage
	fulltext    map[string]json.RawMessage // key -> content as the API returns it
	files       map[string]FileEntry
}

// loadBackup reads a backup folder, or the newest backup in a library folder.
func loadBackup(path string) (*backupSet, error) {
	if _, err := os.Stat(filepath.Join(path, "meta.json")); err != nil {
		latest := latestBackup(path)
		if latest == "" {
			return nil, fmt.Errorf("%s is neither a backup nor a library folder with backups", path)
		}
		path = latest
	}
	b := &backupSet{dir: path, items: map[string]map[string]any{}, collections: map[string]map[string]any{},
		searches: map[string]map[string]any{}, settings: map[string]json.RawMessage{}, fulltext: map[string]json.RawMessage{},
		files: map[string]FileEntry{}}
	if err := readJSON(filepath.Join(path, "meta.json"), &b.meta); err != nil {
		return nil, err
	}
	kind, id, ok := strings.Cut(b.meta.Library, "-")
	if !ok || (kind != "group" && kind != "user") {
		return nil, fmt.Errorf("meta.json: unknown library %q", b.meta.Library)
	}
	b.libPath = "/" + kind + "s/" + id
	for name, dst := range map[string]map[string]map[string]any{"items.json": b.items, "collections.json": b.collections, "searches.json": b.searches} {
		var list []map[string]any
		if err := readJSON(filepath.Join(path, name), &list); err != nil {
			return nil, err
		}
		for _, d := range list {
			if k, _ := d["key"].(string); k != "" {
				dst[k] = d
			}
		}
	}
	readJSON(filepath.Join(path, "settings.json"), &b.settings)
	readJSON(filepath.Join(path, "files.json"), &b.files)
	for k, e := range b.files { // older backups recorded a ZIP's own md5 and no "zip"
		want, _ := b.items[k]["md5"].(string)
		if !e.Zip && want != "" && e.MD5 != want {
			if sum, err := zippedMD5(filepath.Join(path, filepath.FromSlash(e.Path)), e.Filename); err == nil && sum == want {
				e.Zip, e.MD5 = true, want
				b.files[k] = e
			}
		}
	}
	var ft map[string]json.RawMessage
	readJSON(filepath.Join(path, "fulltext.json"), &ft)
	for k, raw := range ft {
		var v struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &v) == nil && len(v.Data) > 0 {
			raw = v.Data // {"version": …, "data": …}
		}
		b.fulltext[k] = raw
	}
	return b, nil
}

// spec is the backup's library as written in a profile: "user" or "group:<id>".
func (b *backupSet) spec() string { return library{ID: b.meta.Library}.Spec() }

// label is the backup's library with its name: group-6719916 "Safe Edit Test".
func (b *backupSet) label() string { return libraryLabel(b.meta.Library, b.meta.Name) }

// libraryNow is the library's current state.
type libraryNow struct {
	version     int
	items       map[string]map[string]any
	collections map[string]map[string]any
	searches    map[string]map[string]any
	settings    map[string]json.RawMessage
}

func readLibrary(c *Client, libPath string) (*libraryNow, error) {
	for try := 1; ; try++ {
		l, err := readLibraryOnce(c, libPath)
		if !errors.Is(err, errLibraryChanged) || try == 3 {
			return l, err
		}
	}
}

func readLibraryOnce(c *Client, libPath string) (*libraryNow, error) {
	l := &libraryNow{}
	versions := map[int]bool{}
	for _, x := range []struct {
		path string
		dst  *map[string]map[string]any
	}{{"/items?includeTrashed=1", &l.items}, {"/collections", &l.collections}, {"/searches", &l.searches}} {
		objs, v, err := getAll[object](c, libPath+x.path)
		if err != nil {
			return nil, err
		}
		versions[v] = true
		*x.dst = map[string]map[string]any{}
		for _, o := range objs {
			var d map[string]any
			if err := json.Unmarshal(o.Data, &d); err != nil {
				return nil, err
			}
			(*x.dst)[o.Key] = d
		}
	}
	body, h, err := c.get(libPath + "/settings")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &l.settings); err != nil {
		return nil, err
	}
	v, _ := strconv.Atoi(h.Get("Last-Modified-Version"))
	versions[v] = true
	if len(versions) != 1 {
		return nil, errLibraryChanged
	}
	l.version = v
	return l, nil
}

// serverManaged are fields the server sets; they are not compared and not written.
var serverManaged = []string{"version", "dateModified", "md5", "mtime"}

func trashed(d map[string]any) bool {
	switch v := d["deleted"].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	}
	return false
}

// differingFields lists the fields of b and s that differ, ignoring
// server-managed ones; being in the trash counts as the field "deleted".
func differingFields(b, s map[string]any) []string {
	var out []string
	for k := range s {
		if _, ok := b[k]; !ok {
			b = maps.Clone(b)
			b[k] = nil
		}
	}
	for k := range b {
		if k != "deleted" && !slices.Contains(serverManaged, k) && !sameValue(k, b[k], s[k]) {
			out = append(out, k)
		}
	}
	if trashed(b) != trashed(s) {
		out = append(out, "deleted")
	}
	sort.Strings(out)
	return out
}

// sameValue: empty and absent are the same; collections and tags have no order
// (Zotero desktop lists them in its own order, the server alphabetically).
func sameValue(k string, a, b any) bool {
	if isEmpty(a) && isEmpty(b) {
		return true
	}
	if k == "collections" || k == "tags" {
		return slices.Equal(sortedJSON(a), sortedJSON(b))
	}
	return reflect.DeepEqual(a, b)
}

func sortedJSON(v any) []string {
	l, _ := v.([]any)
	out := make([]string, len(l))
	for i, x := range l {
		j, _ := json.Marshal(x)
		out[i] = string(j)
	}
	slices.Sort(out)
	return out
}

func emptyLike(v any) any {
	switch v.(type) {
	case []any:
		return []any{}
	case map[string]any:
		return map[string]any{}
	case bool:
		return false
	}
	return ""
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// change is one difference between backup and library.
type change struct {
	Kind   string // items, collections, searches
	Key    string
	What   string // missing, changed, extra (on the server only)
	Fields []string
	backup map[string]any
	server map[string]any
}

// plan is everything restore would do.
type plan struct {
	objects  []change // missing or changed, in write order
	extra    []change // on the server, not in the backup: reported, kept unless removed
	remove   []change // with --trash-new / --erase-new: the extra objects that go
	mode     string   // "", "trash" or "erase"
	files    []string // attachment keys whose file must be uploaded
	settings []string // setting names that differ
	missing  []string // files the backup does not have (missing on the server back then)
}

func (p plan) empty() bool { return len(p.objects)+len(p.files)+len(p.settings)+len(p.remove) == 0 }

// makePlan compares backup and library. Only the selected item keys (and what
// they need) are considered when sel is not nil.
func makePlan(b *backupSet, now *libraryNow, exclude []string, sel []string) plan {
	// what the backup left out is not "not in the backup": never list or remove it
	exclude = append(slices.Clone(exclude), b.meta.Exclude...)
	ex := func(s string) bool { return slices.Contains(exclude, s) }
	skip := func(d map[string]any) bool {
		t, _ := d["itemType"].(string)
		return (ex("trash") && trashed(d)) || (ex("notes") && t == "note") ||
			((ex("annotations") || ex("attachments")) && t == "annotation") || (ex("attachments") && t == "attachment")
	}
	items, cols, searches := b.items, b.collections, b.searches
	if sel != nil {
		items, cols, searches = selection(b, sel), map[string]map[string]any{}, map[string]map[string]any{}
		for _, d := range items {
			for _, ck := range anyStrings(d["collections"]) {
				for ck != "" && b.collections[ck] != nil && cols[ck] == nil {
					cols[ck] = b.collections[ck]
					ck, _ = b.collections[ck]["parentCollection"].(string)
				}
			}
		}
	}

	var p plan
	compare := func(kind string, objs, server map[string]map[string]any, depth func(string) int) {
		field := "parentItem"
		if kind == "collections" {
			field = "parentCollection"
		}
		var keys []string
		for k, d := range objs {
			if !skip(d) {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			if di, dj := depth(keys[i]), depth(keys[j]); di != dj {
				return di < dj
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			s, ok := server[k]
			switch {
			case !ok:
				p.objects = append(p.objects, change{kind, k, "missing", nil, objs[k], nil})
			default:
				if f := differingFields(objs[k], s); len(f) > 0 {
					p.objects = append(p.objects, change{kind, k, "changed", f, objs[k], s})
				}
			}
		}
		if sel == nil {
			var extra []string
			for k := range server {
				if objs[k] == nil && !skip(server[k]) && !(ex("trash") && trashedWithParents(server, k, field)) {
					extra = append(extra, k)
				}
			}
			sort.Strings(extra)
			for _, k := range extra {
				p.extra = append(p.extra, change{kind, k, "extra", nil, nil, server[k]})
			}
		}
	}
	compare("collections", cols, now.collections, func(k string) int { return depthOf(b.collections, k, "parentCollection") })
	compare("searches", searches, now.searches, func(string) int { return 0 })
	compare("items", items, now.items, func(k string) int { return depthOf(b.items, k, "parentItem") })

	if !ex("files") && !ex("attachments") {
		for _, k := range sortedKeys(items) {
			d := items[k]
			if skip(d) || d["itemType"] != "attachment" {
				continue
			}
			if lm, _ := d["linkMode"].(string); lm != "imported_file" && lm != "imported_url" {
				continue
			}
			f, ok := b.files[k]
			if !ok {
				p.missing = append(p.missing, k)
				continue
			}
			if s := now.items[k]; s == nil || s["md5"] != f.MD5 {
				p.files = append(p.files, k)
			}
		}
	}
	if sel == nil && !ex("settings") {
		for name, raw := range b.settings {
			var bv, sv struct {
				Value any `json:"value"`
			}
			json.Unmarshal(raw, &bv)
			json.Unmarshal(now.settings[name], &sv)
			if !reflect.DeepEqual(bv.Value, sv.Value) {
				p.settings = append(p.settings, name)
			}
		}
		sort.Strings(p.settings)
	}
	return p
}

// trashedWithParents tells whether object k is in the trash: itself, or through a parent
// (the children of a trashed item have no "deleted" of their own).
func trashedWithParents(objs map[string]map[string]any, k, field string) bool {
	for n := 0; objs[k] != nil && n < 100; n++ {
		if trashed(objs[k]) {
			return true
		}
		k, _ = objs[k][field].(string)
	}
	return false
}

// withRemovals adds what --trash-new ("trash") or --erase-new ("erase") removes:
// objects in the library but not in the backup (p.extra). Trash leaves what is
// already in the trash; erase takes it too, so the library ends as in the backup.
func withRemovals(p plan, now *libraryNow, mode string) plan {
	p.mode = mode
	if mode == "" {
		return p
	}
	objs := map[string]map[string]map[string]any{"items": now.items, "collections": now.collections, "searches": now.searches}
	for _, c := range p.extra {
		field := "parentItem"
		if c.Kind == "collections" {
			field = "parentCollection"
		}
		if mode == "erase" || !trashedWithParents(objs[c.Kind], c.Key, field) {
			p.remove = append(p.remove, c)
		}
	}
	return p
}

// removeNew trashes or erases p.remove. It runs after the backup's objects were
// written, so an object moved back out of a new parent is no longer under it.
// Trash writes "deleted": true, for items only on the topmost ones (children go
// to the trash with their item, as in Zotero desktop). Erase never erases an
// object that has a backup object below it (the server erases children with
// their parent), as the library is after the writes: backup objects have their
// backup parent, unless their write failed.
func removeNew(c *Client, b *backupSet, now *libraryNow, p plan, failed map[string]bool, log io.Writer) int {
	failures := 0
	going := map[string]bool{}
	for _, ch := range p.remove {
		going[ch.Key] = true
	}
	for _, kind := range []string{"items", "collections", "searches"} {
		server := map[string]map[string]map[string]any{"items": now.items, "collections": now.collections, "searches": now.searches}[kind]
		backup := map[string]map[string]map[string]any{"items": b.items, "collections": b.collections, "searches": b.searches}[kind]
		field := "parentItem"
		if kind == "collections" {
			field = "parentCollection"
		}
		var batch []map[string]any
		var keys []string
		for _, ch := range p.remove {
			if ch.Kind != kind {
				continue
			}
			d := server[ch.Key]
			if parent, _ := d[field].(string); p.mode == "trash" && kind == "items" && going[parent] {
				continue
			}
			if p.mode == "erase" {
				if k := backupBelow(ch.Key, field, backup, server, failed); k != "" {
					failures++
					fmt.Fprintf(log, "FAILED %s %s: not erased, %s below it is in the backup\n", kind, ch.Key, k)
					continue
				}
			}
			keys = append(keys, ch.Key)
			batch = append(batch, map[string]any{"key": ch.Key, "version": d["version"], "deleted": true})
		}
		if len(keys) == 0 {
			continue
		}
		if p.mode == "trash" {
			res, err := c.writeObjects(b.libPath, kind, batch)
			if err != nil {
				fmt.Fprintf(log, "FAILED moving %s not in the backup to the trash: %v\n", kind, err)
				failures++
				continue
			}
			for k, msg := range res.Failed {
				failures++
				fmt.Fprintf(log, "FAILED trash %s %s: %s\n", kind, k, msg)
			}
			fmt.Fprintf(log, "%s not in the backup, moved to the trash: %d\n", kind, len(res.Written)+len(res.Unchanged))
			continue
		}
		if err := c.deleteObjects(b.libPath, kind, keys); err != nil {
			failures++
			fmt.Fprintf(log, "FAILED erasing %s not in the backup: %v\n", kind, err)
			continue
		}
		fmt.Fprintf(log, "%s not in the backup, erased: %d\n", kind, len(keys))
	}
	return failures
}

// backupBelow returns a backup object that lies below k (k is its parent,
// grandparent, …) after the restore's writes, or "": each object has its
// backup parent, unless it is not in the backup or its write failed.
func backupBelow(k, field string, backup, server map[string]map[string]any, failed map[string]bool) string {
	parent := func(o string) string {
		d := backup[o]
		if d == nil || failed[o] {
			d = server[o]
		}
		p, _ := d[field].(string)
		return p
	}
	for bk := range backup {
		for p, n := parent(bk), 0; p != "" && n < 100; p, n = parent(p), n+1 {
			if p == k {
				return bk
			}
		}
	}
	return ""
}

// selection returns the selected items, their descendants and their ancestors.
func selection(b *backupSet, keys []string) map[string]map[string]any {
	out := map[string]map[string]any{}
	children := map[string][]string{}
	for k, d := range b.items {
		if p, _ := d["parentItem"].(string); p != "" {
			children[p] = append(children[p], k)
		}
	}
	var down func(string)
	down = func(k string) {
		if b.items[k] == nil || out[k] != nil {
			return
		}
		out[k] = b.items[k]
		for _, c := range children[k] {
			down(c)
		}
	}
	for _, k := range keys {
		down(k)
		for p, _ := b.items[k]["parentItem"].(string); p != "" && b.items[p] != nil; p, _ = b.items[p]["parentItem"].(string) {
			out[p] = b.items[p]
		}
	}
	return out
}

func depthOf(objs map[string]map[string]any, k, parentField string) int {
	n := 0
	for p, _ := objs[k][parentField].(string); p != "" && objs[p] != nil && n < 100; p, _ = objs[p][parentField].(string) {
		n++
	}
	return n
}

func anyStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var tags = regexp.MustCompile(`<[^>]*>`)

// label is a short human description of an object.
func label(d map[string]any) string {
	for _, f := range []string{"title", "name", "filename", "annotationText", "annotationComment", "note", "annotationType"} {
		if s, _ := d[f].(string); s != "" {
			s = strings.Join(strings.Fields(tags.ReplaceAllString(s, " ")), " ")
			if r := []rune(s); len(r) > 60 {
				s = string(r[:59]) + "…"
			}
			return s
		}
	}
	return ""
}

func typeOf(c change) string {
	d := c.backup
	if d == nil {
		d = c.server
	}
	if t, _ := d["itemType"].(string); t != "" {
		return t
	}
	return strings.TrimSuffix(c.Kind, "s")
}

// printPlan describes the plan; limit caps the lines per section (0: all).
func printPlan(out io.Writer, b *backupSet, now *libraryNow, p plan, limit int) {
	fmt.Fprintf(out, "backup   %s (library version %d)\n", b.dir, b.meta.Version)
	fmt.Fprintf(out, "library  %s, now at version %d\n\n", b.label(), now.version)
	section := func(title string, cs []change) { printSection(out, title, cs, limit) }
	var missing, changed []change
	for _, c := range p.objects {
		if c.What == "missing" {
			missing = append(missing, c)
		} else {
			changed = append(changed, c)
		}
	}
	section("missing in the library (restore recreates them with their keys)", missing)
	section("different in the library (restore sets them back)", changed)
	if len(p.files) > 0 {
		fmt.Fprintf(out, "files to upload: %d\n", len(p.files))
		for i, k := range p.files {
			if limit > 0 && i == limit {
				fmt.Fprintf(out, "  … and %d more\n", len(p.files)-limit)
				break
			}
			fmt.Fprintf(out, "  %s  %s\n", k, b.files[k].Filename)
		}
		fmt.Fprintln(out)
	}
	if len(p.settings) > 0 {
		fmt.Fprintf(out, "settings to set back: %s\n\n", strings.Join(p.settings, ", "))
	}
	if len(p.missing) > 0 {
		fmt.Fprintf(out, "files not in the backup (not on the server when it was made): %d\n\n", len(p.missing))
	}
	switch p.mode {
	case "":
		section("only in the library, not in the backup (restore keeps them)", p.extra)
	default:
		title := "not in the backup: moved to the trash (--trash-new)"
		if p.mode == "erase" {
			title = "not in the backup: ERASED FOR GOOD (--erase-new)"
		}
		section(title, p.remove)
		var kept []change
		for _, c := range p.extra {
			if !slices.ContainsFunc(p.remove, func(r change) bool { return r.Kind == c.Kind && r.Key == c.Key }) {
				kept = append(kept, c)
			}
		}
		section("only in the library, already in the trash (kept)", kept)
	}
	if p.empty() {
		fmt.Fprintln(out, "The library matches the backup.")
	}
}

// printSection prints a titled list of changes with counts per type; limit caps
// the lines (0: all).
func printSection(out io.Writer, title string, cs []change, limit int) {
	if len(cs) == 0 {
		return
	}
	count := map[string]int{}
	for _, c := range cs {
		count[typeOf(c)]++
	}
	var parts []string
	for _, t := range sortedKeys(count) {
		parts = append(parts, fmt.Sprintf("%d %s", count[t], t))
	}
	fmt.Fprintf(out, "%s: %d (%s)\n", title, len(cs), strings.Join(parts, ", "))
	for i, c := range cs {
		if limit > 0 && i == limit {
			fmt.Fprintf(out, "  … and %d more\n", len(cs)-limit)
			break
		}
		d := c.backup
		if d == nil {
			d = c.server
		}
		extra := ""
		if len(c.Fields) > 0 {
			extra = "  [" + strings.Join(c.Fields, ", ") + "]"
		}
		fmt.Fprintf(out, "  %s  %-16s %s%s\n", c.Key, typeOf(c), label(d), extra)
	}
	fmt.Fprintln(out)
}

// restore applies the plan. It writes collections, searches and items in plan
// order, then files, settings and full text, and returns the number of failures.
func restore(c *Client, b *backupSet, now *libraryNow, p plan, exclude []string, log io.Writer, pr *progress) (int, error) {
	failures := 0
	created := map[string]bool{}
	failed := map[string]bool{} // keys whose write failed: still as in the library
	payload := func(ch change) map[string]any {
		d := map[string]any{}
		for k, v := range ch.backup {
			if !slices.Contains(serverManaged, k) {
				d[k] = v
			}
		}
		if ch.server == nil {
			d["version"] = 0
		} else {
			d["version"] = ch.server["version"]
			d["deleted"] = trashed(ch.backup) // explicit: leaving it out would not take it out of the trash
			for k, v := range ch.server {     // a write only touches the fields it names: clear the ones the backup lacks
				if _, ok := d[k]; !ok && !slices.Contains(serverManaged, k) && !isEmpty(v) {
					d[k] = emptyLike(v)
				}
			}
		}
		return d
	}
	// write in groups of the same kind and depth, so parents exist before children
	pr.Phase("objects", len(p.objects))
	for i := 0; i < len(p.objects); {
		j := i
		var batch []map[string]any
		for j < len(p.objects) && p.objects[j].Kind == p.objects[i].Kind && sameDepth(b, p.objects[i], p.objects[j]) {
			batch = append(batch, payload(p.objects[j]))
			j++
		}
		res, err := c.writeObjects(b.libPath, p.objects[i].Kind, batch)
		if err != nil {
			return failures, err
		}
		for _, k := range res.Written {
			for _, ch := range p.objects[i:j] {
				if ch.Key == k && ch.server == nil {
					created[k] = true
				}
			}
		}
		for k, msg := range res.Failed {
			failures++
			failed[k] = true
			fmt.Fprintf(log, "FAILED %s %s: %s\n", p.objects[i].Kind, k, msg)
		}
		fmt.Fprintf(log, "%s: %d written, %d unchanged, %d failed\n", p.objects[i].Kind, len(res.Written), len(res.Unchanged), len(res.Failed))
		i = j
		pr.Set(i, 0, "")
	}
	pr.Done()
	failures += touchSubcollections(c, b, now, p, log)

	uploaded := map[string]bool{}
	if len(p.files) > 0 {
		pr.Phase("files", len(p.files))
	}
	for n, k := range p.files {
		pr.Set(n, 0, "")
		f := b.files[k]
		serverMD5 := ""
		if s := now.items[k]; s != nil && !created[k] {
			serverMD5, _ = s["md5"].(string)
		}
		mtime, _ := b.items[k]["mtime"].(float64)
		existed, err := c.uploadFile(b.libPath, k, filepath.Join(b.dir, filepath.FromSlash(f.Path)), f.Filename, f.MD5, int64(mtime), serverMD5, f.Zip)
		if err != nil {
			failures++
			fmt.Fprintf(log, "FAILED file %s (%s): %v\n", k, f.Filename, err)
			continue
		}
		uploaded[k] = true
		how := "uploaded"
		if existed {
			how = "already on the server, attached"
		}
		fmt.Fprintf(log, "file %s %s: %s\n", k, f.Filename, how)
	}
	if len(p.files) > 0 {
		pr.Set(len(p.files), 0, "")
		pr.Done()
	}

	if len(p.remove) > 0 {
		failures += removeNew(c, b, now, p, failed, log)
	}

	if len(p.settings) > 0 {
		set := map[string]any{}
		for _, name := range p.settings {
			var v struct {
				Value any `json:"value"`
			}
			json.Unmarshal(b.settings[name], &v)
			set[name] = map[string]any{"value": v.Value}
		}
		_, h, err := c.get(b.libPath + "/settings") // the version moved with the writes above
		if err == nil {
			v, _ := strconv.Atoi(h.Get("Last-Modified-Version"))
			err = c.writeSettings(b.libPath, set, v)
		}
		if err != nil {
			failures++
			fmt.Fprintf(log, "FAILED settings: %v\n", err)
		} else {
			fmt.Fprintf(log, "settings: %s\n", strings.Join(p.settings, ", "))
		}
	}

	if !slices.Contains(exclude, "fulltext") {
		n := 0
		for _, k := range sortedKeys(b.fulltext) {
			if !created[k] && !uploaded[k] {
				continue
			}
			if err := c.writeFulltext(b.libPath, k, b.fulltext[k]); err != nil {
				failures++
				fmt.Fprintf(log, "FAILED full text %s: %v\n", k, err)
				continue
			}
			n++
		}
		if n > 0 {
			fmt.Fprintf(log, "full text: %d\n", n)
		}
	}
	return failures, nil
}

// touchSubcollections works around Zotero desktop: trashing a collection puts
// its subcollections into the trash only locally (only the parent is synced
// as deleted), and a synced un-trash of the parent leaves them there. So each
// subcollection of a collection this restore took out of the trash is written
// twice ("deleted" true, then false): the server ends as in the backup, and
// Zotero desktop downloads the subcollection and takes it out of its trash.
func touchSubcollections(c *Client, b *backupSet, now *libraryNow, p plan, log io.Writer) int {
	untrashed, written := map[string]bool{}, map[string]bool{}
	for _, ch := range p.objects {
		if ch.Kind == "collections" {
			written[ch.Key] = true
			if ch.server != nil && trashed(ch.server) && !trashed(ch.backup) {
				untrashed[ch.Key] = true
			}
		}
	}
	var batch []map[string]any
	for _, k := range sortedKeys(b.collections) {
		s := now.collections[k]
		if s == nil || written[k] || trashed(b.collections[k]) {
			continue // missing or written above: Zotero desktop gets it anyway
		}
		for pk, _ := b.collections[k]["parentCollection"].(string); pk != ""; pk, _ = b.collections[pk]["parentCollection"].(string) {
			if untrashed[pk] {
				batch = append(batch, map[string]any{"key": k, "version": s["version"], "deleted": true})
				break
			}
			if b.collections[pk] == nil {
				break
			}
		}
	}
	if len(batch) == 0 {
		return 0
	}
	res, err := c.writeObjects(b.libPath, "collections", batch)
	if err == nil && len(res.Failed) == 0 && res.Version == 0 {
		err = errors.New("no Last-Modified-Version in the answer; run restore again to take them out of the trash")
	}
	if err == nil && len(res.Failed) == 0 {
		for _, d := range batch {
			d["deleted"], d["version"] = false, res.Version
		}
		res, err = c.writeObjects(b.libPath, "collections", batch)
	}
	if err != nil || len(res.Failed) > 0 {
		fmt.Fprintf(log, "FAILED subcollections of collections taken out of the trash: %v %v\n", err, res.Failed)
		return 1
	}
	fmt.Fprintf(log, "subcollections touched so Zotero desktop takes them out of its trash: %d\n", len(batch))
	return 0
}

func sameDepth(b *backupSet, x, y change) bool {
	field := "parentItem"
	objs := b.items
	if x.Kind == "collections" {
		field, objs = "parentCollection", b.collections
	}
	return depthOf(objs, x.Key, field) == depthOf(objs, y.Key, field)
}

// confirm asks on a terminal; elsewhere it requires --yes.
func confirm(question string) (bool, error) {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false, errors.New("not on a terminal: use --dry-run to look, --yes to restore")
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

// canWrite checks that the key may write to the backup's library.
func canWrite(c *Client, libPath string) error {
	k, err := c.keyInfo()
	if err != nil {
		return err
	}
	kind, id, _ := strings.Cut(strings.TrimPrefix(libPath, "/"), "/")
	n, _ := strconv.Atoi(id)
	if (kind == "users" && (n != k.UserID || !k.Access.User.Write)) || (kind == "groups" && !k.GroupAccess(n).Write) {
		return fmt.Errorf("the API key may not write to %s; restore needs a key with write access", libPath)
	}
	return nil
}

// runRestore is the restore command: plan, show, confirm, apply, check.
func runRestore(clientFor func(string) (*Client, error), path string, p Profile, sel []string, remove string, dryRun, yes bool, out io.Writer, pr *progress) error {
	if remove != "" && sel != nil {
		return errors.New("--trash-new / --erase-new act on the whole library: not with --items")
	}
	b, err := loadBackup(path)
	if err != nil {
		return err
	}
	c, err := clientFor(b.spec())
	if err != nil {
		return err
	}
	if !dryRun {
		if err := canWrite(c, b.libPath); err != nil {
			return fmt.Errorf("%s: %w", b.label(), err)
		}
	}
	now, err := readLibrary(c, b.libPath)
	if err != nil {
		return err
	}
	pl := withRemovals(makePlan(b, now, p.Exclude, sel), now, remove)
	printPlan(out, b, now, pl, 25)
	if dryRun || pl.empty() {
		return nil
	}
	if !yes {
		q := fmt.Sprintf("Restore %d objects, %d files, %d settings into %s?", len(pl.objects), len(pl.files), len(pl.settings), b.label())
		switch {
		case pl.mode == "trash" && len(pl.remove) > 0:
			q = fmt.Sprintf("Restore %d objects, %d files, %d settings and move %d objects not in the backup to the trash in %s?", len(pl.objects), len(pl.files), len(pl.settings), len(pl.remove), b.label())
		case pl.mode == "erase" && len(pl.remove) > 0:
			q = fmt.Sprintf("Restore %d objects, %d files, %d settings and ERASE %d objects not in the backup for good in %s?", len(pl.objects), len(pl.files), len(pl.settings), len(pl.remove), b.label())
		}
		ok, err := confirm(q)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not restored")
		}
	}
	pr.SetPrefix(b.label())
	failures, err := restore(c, b, now, pl, p.Exclude, out, pr)
	if err != nil {
		return err
	}
	after, err := readLibrary(c, b.libPath)
	if err != nil {
		return fmt.Errorf("restored, but checking the result failed: %w", err)
	}
	left := withRemovals(makePlan(b, after, p.Exclude, sel), after, remove)
	if left.empty() && failures == 0 {
		fmt.Fprintln(out, "\nchecked: the library matches the backup")
		return nil
	}
	fmt.Fprintln(out, "\nchecked: still different from the backup:")
	printPlan(out, b, after, left, 25)
	return fmt.Errorf("restore incomplete: %d failures, %d objects / %d files / %d settings still differ (Zotero clients with unsynced changes? run restore again after syncing them)",
		failures, len(left.objects), len(left.files), len(left.settings))
}

// runDiff is the diff command.
func runDiff(clientFor func(string) (*Client, error), path string, p Profile, sel []string, out io.Writer) error {
	b, err := loadBackup(path)
	if err != nil {
		return err
	}
	c, err := clientFor(b.spec())
	if err != nil {
		return err
	}
	now, err := readLibrary(c, b.libPath)
	if err != nil {
		return err
	}
	printPlan(out, b, now, makePlan(b, now, p.Exclude, sel), 0)
	return nil
}
