package main

// Complete, dated backups. Every backup folder holds the whole library; files
// that did not change since the previous backup are hard links to it, so they
// are neither downloaded again nor stored twice.

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	timeLayout = "2006-01-02T150405Z" // backup folder names; no colons (Windows)
	workers    = 4                    // parallel downloads; ponytail: fixed, make it an option if needed
)

// object is one object as the API returns it; only its data is stored.
type object struct {
	Key  string          `json:"key"`
	Data json.RawMessage `json:"data"`
}

// fields are the parts of an object's data the backup needs to look at.
type fields struct {
	ItemType   string `json:"itemType"`
	LinkMode   string `json:"linkMode"`
	MD5        string `json:"md5"`
	Filename   string `json:"filename"`
	ParentItem string `json:"parentItem"`
	Deleted    any    `json:"deleted"` // 1 / true when in the trash
}

func (o object) fields() fields {
	var f fields
	json.Unmarshal(o.Data, &f)
	return f
}

func inTrash(f fields) bool {
	switch v := f.Deleted.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	}
	return false
}

// library is one Zotero library to back up.
type library struct {
	Path string // API path, e.g. /groups/6719916
	ID   string // e.g. group-6719916; identifies the folder even if the name changes
	Name string
}

// Spec is the library as written in a profile: "user" or "group:<id>".
func (l library) Spec() string {
	if strings.HasPrefix(l.ID, "group-") {
		return "group:" + strings.TrimPrefix(l.ID, "group-")
	}
	return "user"
}

// Dir is the library's folder name, e.g. "group-6719916-Safe_Edit_Test":
// the name made safe, without spaces.
func (l library) Dir() string {
	if n := strings.ReplaceAll(safeName(l.Name, ""), " ", "_"); n != "" {
		return l.ID + "-" + n
	}
	return l.ID
}

// libraryFolder returns the library's folder below root, renaming an existing
// folder of the same library whose name part is out of date (group renamed).
func libraryFolder(root string, l library) (string, error) {
	want := filepath.Join(root, l.Dir())
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() && n != l.Dir() && (n == l.ID || strings.HasPrefix(n, l.ID+"-")) {
			if _, err := os.Stat(want); err == nil {
				return "", fmt.Errorf("both %s and %s exist for %s; merge or remove one", n, l.Dir(), l.ID)
			}
			if err := os.Rename(filepath.Join(root, n), want); err != nil {
				return "", err
			}
			return want, nil
		}
	}
	return want, os.MkdirAll(want, 0o755)
}

// FileEntry records one attachment file in a backup.
type FileEntry struct {
	Filename string `json:"filename"` // name in Zotero
	Path     string `json:"path"`     // relative to the backup folder
	MD5      string `json:"md5"`
	Size     int64  `json:"size"`
	Source   string `json:"source"` // downloaded, linked or resumed
}

type MissingFile struct {
	Key        string `json:"key"`
	Filename   string `json:"filename"`
	ParentItem string `json:"parentItem"`
	Reason     string `json:"reason"`
}

// Meta is written as meta.json into every backup.
type Meta struct {
	Library        string         `json:"library"`
	Name           string         `json:"name"`
	Version        int            `json:"version"`
	Date           string         `json:"date"`
	Exclude        []string       `json:"exclude"`
	Previous       string         `json:"previous,omitempty"`
	Counts         map[string]int `json:"counts"`
	Downloaded     int            `json:"files_downloaded"`
	Linked         int            `json:"files_linked"`
	FulltextReused int            `json:"fulltext_reused"`
	DownBytes      int64          `json:"bytes_downloaded"`
	TotalBytes     int64          `json:"bytes_total"`
	Missing        []MissingFile  `json:"missing_files"`
	Problems       []string       `json:"problems"`
	Seconds        float64        `json:"seconds"`
}

// resolveLibraries turns the profile's library spec into libraries the key can read.
// For "all" it also returns the libraries left out and why.
func resolveLibraries(c *Client, spec string) (libs []library, skipped []string, err error) {
	k, err := c.keyInfo()
	if err != nil {
		return nil, nil, err
	}
	kind, gid, err := parseLibrary(spec)
	if err != nil {
		return nil, nil, err
	}
	user := library{fmt.Sprintf("/users/%d", k.UserID), fmt.Sprintf("user-%d", k.UserID), k.Username}
	if kind == "user" || kind == "all" {
		if k.Access.User.Library {
			libs = append(libs, user)
		} else if kind == "user" {
			return nil, nil, errors.New("the key has no access to the personal library")
		} else {
			skipped = append(skipped, user.ID+": no access")
		}
	}
	if kind == "user" {
		return libs, skipped, nil
	}
	groups, err := c.groups(k.UserID)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range groups {
		if kind == "group" && g.ID != gid {
			continue
		}
		l := library{fmt.Sprintf("/groups/%d", g.ID), fmt.Sprintf("group-%d", g.ID), g.Data.Name}
		if k.GroupAccess(g.ID).Library {
			libs = append(libs, l)
		} else if kind == "group" {
			return nil, nil, fmt.Errorf("the key has no access to %s (%s)", spec, g.Data.Name)
		} else {
			skipped = append(skipped, l.ID+" ("+g.Data.Name+"): no access")
		}
	}
	if kind == "group" && len(libs) == 0 {
		return nil, nil, fmt.Errorf("%s: the key's user is not a member of this group", spec)
	}
	return libs, skipped, nil
}

// snapshot is the metadata of one library at one version.
type snapshot struct {
	version     int
	items       []object
	collections []object
	searches    []object
	settings    json.RawMessage
	fulltext    map[string]FulltextEntry
	ftReused    int
}

// FulltextEntry is one attachment's full-text content with the version the
// index gave it, so an unchanged entry can be taken from the previous backup.
type FulltextEntry struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// readMetadata reads everything but the files, retrying when the library
// changes while it is being read. Full-text entries whose version did not
// change since the previous backup (prevFT) are taken from it.
func readMetadata(c *Client, lib library, exclude []string, prevFT map[string]FulltextEntry, pr *progress) (snapshot, error) {
	for try := 1; ; try++ {
		s, err := readMetadataOnce(c, lib, exclude, prevFT, pr)
		if !errors.Is(err, errLibraryChanged) || try == 3 {
			return s, err
		}
	}
}

func readMetadataOnce(c *Client, lib library, ex []string, prevFT map[string]FulltextEntry, pr *progress) (snapshot, error) {
	var s snapshot
	versions := map[int]bool{}
	itemsPath := lib.Path + "/items?includeTrashed=1"
	if slices.Contains(ex, "trash") {
		itemsPath = lib.Path + "/items"
	}
	pr.Phase("items", 0)
	items, v, err := getAll[object](c, itemsPath, func(d, t int) { pr.Set(d, t, "") })
	pr.Done()
	if err != nil {
		return s, err
	}
	versions[v] = true
	if s.collections, v, err = getAll[object](c, lib.Path+"/collections"); err != nil {
		return s, err
	}
	versions[v] = true
	if s.searches, v, err = getAll[object](c, lib.Path+"/searches"); err != nil {
		return s, err
	}
	versions[v] = true
	s.settings = json.RawMessage("{}")
	if !slices.Contains(ex, "settings") {
		body, h, err := c.get(lib.Path + "/settings")
		if err != nil {
			return s, err
		}
		s.settings = body
		v, _ = strconv.Atoi(h.Get("Last-Modified-Version"))
		versions[v] = true
	}
	var ftIndex map[string]int // key -> version
	if !slices.Contains(ex, "fulltext") {
		h, err := c.getJSON(lib.Path+"/fulltext?since=0", &ftIndex)
		if err != nil {
			return s, err
		}
		v, _ = strconv.Atoi(h.Get("Last-Modified-Version"))
		versions[v] = true
	}
	if len(versions) != 1 {
		return s, errLibraryChanged
	}
	s.version = v

	drop := map[string]bool{}
	if slices.Contains(ex, "notes") {
		drop["note"] = true
	}
	if slices.Contains(ex, "annotations") || slices.Contains(ex, "attachments") {
		drop["annotation"] = true
	}
	if slices.Contains(ex, "attachments") {
		drop["attachment"] = true
	}
	kept := map[string]bool{}
	for _, it := range items {
		if !drop[it.fields().ItemType] {
			s.items = append(s.items, it)
			kept[it.Key] = true
		}
	}
	if slices.Contains(ex, "trash") {
		s.collections = slices.DeleteFunc(s.collections, func(o object) bool { return inTrash(o.fields()) })
		s.searches = slices.DeleteFunc(s.searches, func(o object) bool { return inTrash(o.fields()) })
	}

	s.fulltext = map[string]FulltextEntry{}
	var keys []string
	for k, v := range ftIndex {
		if !kept[k] {
			continue
		}
		if p, ok := prevFT[k]; ok && p.Version == v && len(p.Data) > 0 {
			s.fulltext[k] = p
			s.ftReused++
		} else {
			keys = append(keys, k)
		}
	}
	var mu sync.Mutex
	done := 0
	if len(keys) == 0 {
		if len(ftIndex) > 0 {
			pr.Logf("full text: all %d entries unchanged, taken from the previous backup", s.ftReused)
		}
		return s, nil
	}
	pr.Phase("full text", len(keys))
	err = parallel(keys, func(k string) error {
		body, _, err := c.get(lib.Path + "/items/" + k + "/fulltext")
		var he *HTTPError
		if errors.As(err, &he) && he.Code == 404 { // index entry without content: nothing to keep
			body, err = nil, nil
		}
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if body != nil {
			s.fulltext[k] = FulltextEntry{ftIndex[k], body}
		}
		done++
		pr.Set(done, 0, fmt.Sprintf("%d unchanged, taken from the previous backup", s.ftReused))
		return nil
	})
	pr.Done()
	return s, err
}

// parallel runs fn for every key with a fixed number of workers and returns the first error.
func parallel(keys []string, fn func(string) error) error {
	ch := make(chan string)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range ch {
				if err := fn(k); err != nil {
					once.Do(func() { first = err })
				}
			}
		}()
	}
	for _, k := range keys {
		ch <- k
	}
	close(ch)
	wg.Wait()
	return first
}

// backupLibrary writes one complete backup of lib below root and
// returns its folder (root/<ID>-<name>/<date>). It is written as <date>.partial and renamed when complete;
// a .partial left by an interrupted run is reused.
func backupLibrary(c *Client, lib library, root string, exclude []string, now time.Time, pr *progress) (string, Meta, error) {
	start := time.Now()
	libRoot, err := libraryFolder(root, lib)
	if err != nil {
		return "", Meta{}, err
	}
	prev := latestBackup(libRoot)
	work := findPartial(libRoot)
	if work == "" {
		work = filepath.Join(libRoot, now.UTC().Format(timeLayout)+".partial")
		if err := os.Mkdir(work, 0o755); err != nil {
			return "", Meta{}, err
		}
	} else {
		pr.Logf("resuming %s", filepath.Base(work))
	}

	prevFT := map[string]FulltextEntry{}
	if prev != "" {
		readJSON(filepath.Join(prev, "fulltext.json"), &prevFT) // older backups without versions: nothing reused
	}
	s, err := readMetadata(c, lib, exclude, prevFT, pr)
	if err != nil {
		return "", Meta{}, err
	}
	m := Meta{
		Library: lib.ID, Name: lib.Name, Version: s.version, Date: now.UTC().Format(time.RFC3339),
		Exclude: exclude, Counts: map[string]int{}, Missing: []MissingFile{}, Problems: []string{},
	}
	if prev != "" {
		m.Previous = filepath.Base(prev)
	}
	for _, it := range s.items {
		m.Counts[it.fields().ItemType]++
	}
	m.Counts["collections"] = len(s.collections)
	m.Counts["searches"] = len(s.searches)
	m.Counts["fulltext"] = len(s.fulltext)
	m.FulltextReused = s.ftReused

	datas := func(objs []object) []json.RawMessage {
		out := make([]json.RawMessage, len(objs))
		for i, o := range objs {
			out[i] = o.Data
		}
		return out
	}
	for name, v := range map[string]any{
		"items.json": datas(s.items), "collections.json": datas(s.collections),
		"searches.json": datas(s.searches), "settings.json": s.settings, "fulltext.json": s.fulltext,
	} {
		if err := writeJSON(filepath.Join(work, name), v); err != nil {
			return "", Meta{}, err
		}
	}

	files := map[string]FileEntry{}
	if !slices.Contains(exclude, "files") && !slices.Contains(exclude, "attachments") {
		if files, err = backupFiles(c, lib, s.items, work, prev, &m, pr); err != nil {
			return "", Meta{}, err
		}
	}
	if err := writeJSON(filepath.Join(work, "files.json"), files); err != nil {
		return "", Meta{}, err
	}
	m.Counts["files"] = len(files)
	m.Seconds = time.Since(start).Round(100 * time.Millisecond).Seconds()
	if err := writeJSON(filepath.Join(work, "meta.json"), m); err != nil {
		return "", Meta{}, err
	}
	final := filepath.Join(libRoot, now.UTC().Format(timeLayout))
	if err := os.Rename(work, final); err != nil {
		return "", Meta{}, err
	}
	return final, m, nil
}

// backupFiles puts every stored attachment file into work/files: a hard link
// when the previous backup (or this one) already has the same content, else a
// download checked against the md5 Zotero records.
func backupFiles(c *Client, lib library, items []object, work, prev string, m *Meta, pr *progress) (map[string]FileEntry, error) {
	prevFiles := map[string]FileEntry{}
	if prev != "" {
		readJSON(filepath.Join(prev, "files.json"), &prevFiles)
	}
	var mu sync.Mutex
	byMD5 := map[string]string{} // md5 -> existing file with that content
	for _, e := range prevFiles {
		byMD5[e.MD5] = filepath.Join(prev, e.Path)
	}
	files := map[string]FileEntry{}
	atts := map[string]fields{}
	var keys []string
	for _, it := range items {
		f := it.fields()
		if f.ItemType == "attachment" && (f.LinkMode == "imported_file" || f.LinkMode == "imported_url") {
			atts[it.Key] = f
			keys = append(keys, it.Key)
		}
	}
	sort.Strings(keys)
	done := 0
	pr.Phase("files", len(keys))

	// Files with the same content as an earlier one in this run are done in a
	// second pass, so they become links instead of parallel duplicate downloads.
	var first, again []string
	seen := map[string]bool{}
	for _, k := range keys {
		if m := atts[k].MD5; m != "" && seen[m] {
			again = append(again, k)
		} else {
			seen[m] = true
			first = append(first, k)
		}
	}
	one := func(key string) error {
		f := atts[key]
		rel := filepath.Join("files", key, safeName(f.Filename, key))
		dst := filepath.Join(work, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		e := FileEntry{Filename: f.Filename, Path: filepath.ToSlash(rel), MD5: f.MD5}
		record := func() {
			mu.Lock()
			defer mu.Unlock()
			files[key] = e
			m.TotalBytes += e.Size
			if e.Source == "downloaded" {
				m.Downloaded++
				m.DownBytes += e.Size
			} else {
				m.Linked++
			}
			byMD5[e.MD5] = dst
			done++
			pr.Set(done+len(m.Missing), 0, fmt.Sprintf("%d linked, %d downloaded (%.0f MB)", m.Linked, m.Downloaded, float64(m.DownBytes)/1e6))
		}

		if f.MD5 != "" {
			if sum, size, err := md5File(dst); err == nil && sum == f.MD5 { // left by an interrupted run
				e.Size, e.Source = size, "resumed"
				record()
				return nil
			}
			mu.Lock()
			src := byMD5[f.MD5]
			mu.Unlock()
			if src != "" {
				os.Remove(dst)
				if err := linkOrCopy(src, dst); err == nil {
					st, _ := os.Stat(dst)
					e.Size, e.Source = st.Size(), "linked"
					record()
					return nil
				}
			}
		}

		sum, size, err := download(c, lib.Path+"/items/"+key+"/file", dst)
		var he *HTTPError
		if errors.As(err, &he) && he.Code == 404 {
			reason := "file not on the server"
			if f.MD5 != "" {
				reason = "file on record (md5 set) but not downloadable"
			}
			mu.Lock()
			m.Missing = append(m.Missing, MissingFile{key, f.Filename, f.ParentItem, reason})
			mu.Unlock()
			return nil
		}
		if err != nil {
			return fmt.Errorf("file %s: %w", key, err)
		}
		if f.MD5 == "" {
			e.MD5 = sum
		} else if sum != f.MD5 {
			mu.Lock()
			m.Problems = append(m.Problems, fmt.Sprintf("%s: downloaded md5 %s differs from Zotero's %s", key, sum, f.MD5))
			mu.Unlock()
			e.MD5 = sum
		}
		e.Size, e.Source = size, "downloaded"
		record()
		return nil
	}
	err := parallel(first, one)
	if err == nil {
		err = parallel(again, one)
	}
	pr.Done()
	sort.Slice(m.Missing, func(i, j int) bool { return m.Missing[i].Key < m.Missing[j].Key })
	sort.Strings(m.Problems)
	return files, err
}

// download streams a file to dst (via dst.tmp) and returns its md5 and size.
func download(c *Client, path, dst string) (string, int64, error) {
	tmp := dst + ".tmp"
	var sum string
	var size int64
	_, err := c.do(path, func(r io.Reader) error {
		f, err := os.Create(tmp) // truncates on retry
		if err != nil {
			return err
		}
		defer f.Close()
		h := md5.New()
		size, err = io.Copy(io.MultiWriter(f, h), r)
		if err != nil {
			return err
		}
		sum = hex.EncodeToString(h.Sum(nil))
		return f.Close()
	})
	if err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	return sum, size, os.Rename(tmp, dst)
}

// linkOrCopy hard-links src to dst, or copies it where links are impossible
// (FAT / exFAT, some network shares, NTFS's limit of 1023 links per file).
func linkOrCopy(src, dst string) error {
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func md5File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// safeName makes a Zotero file name usable on Windows, macOS and Linux.
func safeName(name, key string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = key
	}
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	if slices.Contains([]string{"CON", "PRN", "AUX", "NUL"}, base) ||
		(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
		name = "_" + name
	}
	if len(name) > 150 { // keep paths well within limits; keep the extension
		ext := filepath.Ext(name)
		if len(ext) > 10 {
			ext = ""
		}
		cut := 150 - len(ext)
		for cut > 0 && !utf8RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut] + ext
	}
	return name
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// latestBackup returns the newest complete backup folder in libRoot, or "".
func latestBackup(libRoot string) string {
	all := backups(libRoot)
	if len(all) == 0 {
		return ""
	}
	return filepath.Join(libRoot, all[len(all)-1])
}

// backups lists the complete backup folders in libRoot, oldest first.
func backups(libRoot string) []string {
	entries, _ := os.ReadDir(libRoot)
	var names []string
	for _, e := range entries {
		if _, err := time.Parse(timeLayout, e.Name()); e.IsDir() && err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func findPartial(libRoot string) string {
	entries, _ := os.ReadDir(libRoot)
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".partial") {
			return filepath.Join(libRoot, e.Name())
		}
	}
	return ""
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
