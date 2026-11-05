package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func md5hex(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

// fakeLibrary is group 1 of user 7, served like the Zotero API.
type fakeLibrary struct {
	mu          sync.Mutex
	version     int
	items       map[string]map[string]any // key -> data
	collections map[string]map[string]any
	settings    string
	fulltext    map[string]string // key -> content JSON
	files       map[string]string // key -> file content
	downloads   map[string]int
	failFile    map[string]bool // answer 400 for these files
	bumpOnce    bool            // raise the version once, after the first collections read
}

func newFakeLibrary() *fakeLibrary {
	return &fakeLibrary{
		version: 10, items: map[string]map[string]any{}, collections: map[string]map[string]any{},
		settings: "{}", fulltext: map[string]string{}, files: map[string]string{},
		downloads: map[string]int{}, failFile: map[string]bool{},
	}
}

func (f *fakeLibrary) addFile(key, parent, filename, content string, withMD5 bool) {
	d := map[string]any{"key": key, "itemType": "attachment", "linkMode": "imported_file", "parentItem": parent, "filename": filename}
	if withMD5 {
		d["md5"] = md5hex(content)
		f.files[key] = content
	}
	f.items[key] = d
}

func (f *fakeLibrary) list(w http.ResponseWriter, r *http.Request, objs map[string]map[string]any) {
	var keys []string
	for k, d := range objs {
		if d["deleted"] != nil && r.URL.Query().Get("includeTrashed") != "1" && strings.HasSuffix(r.URL.Path, "/items") {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var page []map[string]any
	for _, k := range keys[min(start, len(keys)):min(start+limit, len(keys))] {
		page = append(page, map[string]any{"key": k, "data": objs[k]})
	}
	w.Header().Set("Total-Results", strconv.Itoa(len(keys)))
	b, _ := json.Marshal(page)
	if page == nil {
		b = []byte("[]")
	}
	w.Write(b)
}

func (f *fakeLibrary) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Last-Modified-Version", strconv.Itoa(f.version))
	p := r.URL.Path
	switch {
	case p == "/keys/current":
		fmt.Fprint(w, `{"userID":7,"username":"tester","access":{"user":{"library":false},"groups":{"all":{"library":true}}}}`)
	case p == "/users/7/groups":
		w.Header().Set("Total-Results", "1")
		fmt.Fprint(w, `[{"id":1,"data":{"name":"Test group"}}]`)
	case p == "/groups/1/items":
		f.list(w, r, f.items)
	case p == "/groups/1/collections":
		f.list(w, r, f.collections)
		if f.bumpOnce {
			f.bumpOnce = false
			f.version++
		}
	case p == "/groups/1/searches":
		f.list(w, r, nil)
	case p == "/groups/1/settings":
		fmt.Fprint(w, f.settings)
	case p == "/groups/1/fulltext":
		idx := map[string]int{}
		for k := range f.fulltext {
			idx[k] = f.version
		}
		json.NewEncoder(w).Encode(idx)
	case strings.HasSuffix(p, "/fulltext"):
		fmt.Fprint(w, f.fulltext[strings.Split(p, "/")[4]])
	case strings.HasSuffix(p, "/file"):
		key := strings.Split(p, "/")[4]
		if f.failFile[key] {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		content, ok := f.files[key]
		if !ok {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		f.downloads[key]++
		io.WriteString(w, content)
	default:
		http.Error(w, "unexpected "+p, http.StatusTeapot)
	}
}

// sampleLibrary has a book with a PDF (Windows-unsafe name), an annotation,
// a note, an attachment whose file never reached the server, a trashed item,
// a collection, tag colours and a full-text entry.
func sampleLibrary() *fakeLibrary {
	f := newFakeLibrary()
	f.items["BOOK0001"] = map[string]any{"key": "BOOK0001", "itemType": "book", "title": "A book", "collections": []string{"COLL0001"}}
	f.addFile("PDF00001", "BOOK0001", `Report: draft?.pdf`, "pdf-one", true)
	f.addFile("NOFILE01", "BOOK0001", "missing.pdf", "", false)
	f.items["ANNO0001"] = map[string]any{"key": "ANNO0001", "itemType": "annotation", "parentItem": "PDF00001"}
	f.items["NOTE0001"] = map[string]any{"key": "NOTE0001", "itemType": "note", "parentItem": "BOOK0001"}
	f.items["TRASH001"] = map[string]any{"key": "TRASH001", "itemType": "book", "deleted": 1}
	f.collections["COLL0001"] = map[string]any{"key": "COLL0001", "name": "C"}
	f.settings = `{"tagColors":{"value":[{"name":"x","color":"#FF6666"}],"version":3}}`
	f.fulltext["PDF00001"] = `{"content":"text","indexedPages":1,"totalPages":1}`
	return f
}

func runFake(t *testing.T, f *fakeLibrary, root string, exclude []string, at time.Time) (string, Meta) {
	t.Helper()
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &Client{Base: srv.URL, Key: "k", HTTP: srv.Client(), Sleep: func(time.Duration) {}}
	libs, _, err := resolveLibraries(c, "group:1")
	if err != nil {
		t.Fatal(err)
	}
	dir, m, err := backupLibrary(c, libs[0], root, exclude, at, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

func day(n int) time.Time { return time.Date(2026, 11, n, 6, 0, 0, 0, time.UTC) }

func readItems(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	var list []map[string]any
	if err := readJSON(filepath.Join(dir, "items.json"), &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, d := range list {
		out[d["key"].(string)] = d
	}
	return out
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		t.Fatalf("stat: %v %v", err1, err2)
	}
	return os.SameFile(sa, sb)
}

func TestBackupCompleteThenLinked(t *testing.T) {
	root := t.TempDir()
	f := sampleLibrary()

	// first backup: everything, the file downloaded
	b1, m1 := runFake(t, f, root, nil, day(1))
	if filepath.Base(b1) != "2026-11-01T060000Z" || filepath.Base(filepath.Dir(b1)) != "group-1-Test_group" {
		t.Errorf("backup folder %s", b1)
	}
	items := readItems(t, b1)
	if len(items) != 6 || items["TRASH001"] == nil || items["NOTE0001"] == nil || items["ANNO0001"] == nil {
		t.Errorf("items.json has %d items: %v", len(items), items)
	}
	pdf := filepath.Join(b1, "files", "PDF00001", "Report_ draft_.pdf")
	if b, err := os.ReadFile(pdf); err != nil || string(b) != "pdf-one" {
		t.Errorf("file: %q %v", b, err)
	}
	var files map[string]FileEntry
	readJSON(filepath.Join(b1, "files.json"), &files)
	if e := files["PDF00001"]; e.Filename != `Report: draft?.pdf` || e.MD5 != md5hex("pdf-one") || e.Source != "downloaded" {
		t.Errorf("files.json entry %+v", e)
	}
	if len(m1.Missing) != 1 || m1.Missing[0].Key != "NOFILE01" || m1.Missing[0].Reason != "file not on the server" {
		t.Errorf("missing %+v", m1.Missing)
	}
	var ft map[string]json.RawMessage
	readJSON(filepath.Join(b1, "fulltext.json"), &ft)
	if len(ft) != 1 || ft["PDF00001"] == nil {
		t.Errorf("fulltext.json %s", ft)
	}
	var settings map[string]any
	readJSON(filepath.Join(b1, "settings.json"), &settings)
	if settings["tagColors"] == nil {
		t.Errorf("settings.json %v", settings)
	}
	if m1.Version != 10 || m1.Downloaded != 1 || m1.Linked != 0 || m1.Counts["collections"] != 1 {
		t.Errorf("meta %+v", m1)
	}

	// second backup: complete again, the unchanged file a hard link, not downloaded
	b2, m2 := runFake(t, f, root, nil, day(2))
	if f.downloads["PDF00001"] != 1 || m2.Linked != 1 || m2.Downloaded != 0 || m2.Previous != filepath.Base(b1) {
		t.Errorf("downloads %v, meta %+v", f.downloads, m2)
	}
	pdf2 := filepath.Join(b2, "files", "PDF00001", "Report_ draft_.pdf")
	if !sameFile(t, pdf, pdf2) {
		t.Error("unchanged file is not a hard link to the previous backup")
	}
	if len(readItems(t, b2)) != 6 {
		t.Error("second backup is not complete")
	}

	// changed file: downloaded again, no longer the same file
	f.addFile("PDF00001", "BOOK0001", `Report: draft?.pdf`, "pdf-two", true)
	b3, m3 := runFake(t, f, root, nil, day(3))
	pdf3 := filepath.Join(b3, "files", "PDF00001", "Report_ draft_.pdf")
	if b, _ := os.ReadFile(pdf3); string(b) != "pdf-two" || m3.Downloaded != 1 || sameFile(t, pdf2, pdf3) {
		t.Errorf("changed file: %q, meta %+v", b, m3)
	}
	if b, _ := os.ReadFile(pdf2); string(b) != "pdf-one" {
		t.Error("the previous backup's file was changed")
	}

	// deleted attachment: gone from the new backup, still in the old ones
	delete(f.items, "PDF00001")
	delete(f.items, "ANNO0001")
	b4, _ := runFake(t, f, root, nil, day(4))
	if _, err := os.Stat(filepath.Join(b4, "files", "PDF00001")); !os.IsNotExist(err) {
		t.Error("deleted attachment's file still in the new backup")
	}
	if _, err := os.Stat(pdf3); err != nil {
		t.Error("old backup lost its file")
	}
	if got := backups(filepath.Join(root, "group-1-Test_group")); len(got) != 4 || findPartial(filepath.Join(root, "group-1-Test_group")) != "" {
		t.Errorf("backups %v", got)
	}
}

func TestBackupSameContentTwiceIsLinked(t *testing.T) {
	f := newFakeLibrary()
	f.items["BOOK0001"] = map[string]any{"key": "BOOK0001", "itemType": "book"}
	f.addFile("PDF00001", "BOOK0001", "a.pdf", "same", true)
	f.addFile("PDF00002", "BOOK0001", "b.pdf", "same", true)
	b, m := runFake(t, f, t.TempDir(), nil, day(1))
	if m.Downloaded+m.Linked != 2 || m.Downloaded != 1 {
		t.Errorf("meta %+v", m)
	}
	if !sameFile(t, filepath.Join(b, "files", "PDF00001", "a.pdf"), filepath.Join(b, "files", "PDF00002", "b.pdf")) {
		t.Error("identical files are not linked")
	}
}

func TestBackupExclude(t *testing.T) {
	f := sampleLibrary()
	b, m := runFake(t, f, t.TempDir(), []string{"files", "notes", "trash", "fulltext", "settings"}, day(1))
	items := readItems(t, b)
	if items["NOTE0001"] != nil || items["TRASH001"] != nil || items["PDF00001"] == nil {
		t.Errorf("items %v", items)
	}
	if len(f.downloads) != 0 || m.Counts["files"] != 0 {
		t.Errorf("files downloaded: %v", f.downloads)
	}
	if _, err := os.Stat(filepath.Join(b, "files")); !os.IsNotExist(err) {
		t.Error("files folder exists")
	}
	var ft map[string]any
	readJSON(filepath.Join(b, "fulltext.json"), &ft)
	var settings map[string]any
	readJSON(filepath.Join(b, "settings.json"), &settings)
	if len(ft) != 0 || len(settings) != 0 {
		t.Errorf("fulltext %v settings %v", ft, settings)
	}

	b2, _ := runFake(t, f, t.TempDir(), []string{"attachments"}, day(1))
	items = readItems(t, b2)
	if items["PDF00001"] != nil || items["ANNO0001"] != nil || items["BOOK0001"] == nil {
		t.Errorf("attachments excluded, items %v", items)
	}
}

func TestBackupResumesAfterFailure(t *testing.T) {
	root := t.TempDir()
	f := sampleLibrary()
	f.addFile("PDF00002", "BOOK0001", "second.pdf", "pdf-two", true)
	f.failFile["PDF00002"] = true

	srv := httptest.NewServer(f)
	c := &Client{Base: srv.URL, Key: "k", HTTP: srv.Client(), Sleep: func(time.Duration) {}}
	libs, _, _ := resolveLibraries(c, "group:1")
	if _, _, err := backupLibrary(c, libs[0], root, nil, day(1), io.Discard); err == nil {
		t.Fatal("backup with a failing file did not fail")
	}
	srv.Close()
	libRoot := filepath.Join(root, "group-1-Test_group")
	if len(backups(libRoot)) != 0 || findPartial(libRoot) == "" {
		t.Fatalf("after failure: backups %v, partial %q", backups(libRoot), findPartial(libRoot))
	}

	f.failFile = map[string]bool{}
	b, m := runFake(t, f, root, nil, day(1).Add(time.Hour))
	if f.downloads["PDF00001"] != 1 || f.downloads["PDF00002"] != 1 {
		t.Errorf("downloads %v: the file from the failed run was fetched again", f.downloads)
	}
	if m.Counts["files"] != 2 || findPartial(libRoot) != "" || filepath.Base(b) != "2026-11-01T070000Z" {
		t.Errorf("meta %+v, partial %q, dir %s", m, findPartial(libRoot), b)
	}
}

func TestBackupRereadsWhenLibraryChanges(t *testing.T) {
	f := sampleLibrary()
	f.bumpOnce = true
	_, m := runFake(t, f, t.TempDir(), nil, day(1))
	if m.Version != 11 {
		t.Errorf("version %d, want 11 (read again after the change)", m.Version)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"plain.pdf":                       "plain.pdf",
		`a<b>c:d"e|f?*.pdf`:               "a_b_c_d_e_f__.pdf",
		"dir/sub\\x.pdf":                  "dir_sub_x.pdf",
		"CON.pdf":                         "_CON.pdf",
		"com1.txt":                        "_com1.txt",
		"trailing. ":                      "trailing",
		"":                                "KEY12345",
		strings.Repeat("ä", 100) + ".pdf": strings.Repeat("ä", 73) + ".pdf",
	} {
		if got := safeName(in, "KEY12345"); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeepThreeGenerations(t *testing.T) {
	// two backups a day for 120 days, from 2026-01-01
	var names []string
	for d := 0; d < 120; d++ {
		for _, h := range []int{6, 18} {
			names = append(names, time.Date(2026, 1, 1+d, h, 0, 0, 0, time.UTC).Format(timeLayout))
		}
	}
	kept := keep(names, 7, 5, 12)
	var got []string
	for _, n := range names {
		if kept[n] {
			got = append(got, n)
		}
	}
	// newest per day for 7 days (Apr 24–30), per ISO week for 5 weeks (Apr 30 in
	// the current week, then Sundays Apr 26, 19, 12, 5), per month for the 4 months
	// there are (Apr 30, Mar 31, Feb 28, Jan 31); all at 18:00
	want := []string{
		"2026-01-31T180000Z", "2026-02-28T180000Z", "2026-03-31T180000Z",
		"2026-04-05T180000Z", "2026-04-12T180000Z", "2026-04-19T180000Z",
		"2026-04-24T180000Z", "2026-04-25T180000Z", "2026-04-26T180000Z", "2026-04-27T180000Z",
		"2026-04-28T180000Z", "2026-04-29T180000Z", "2026-04-30T180000Z",
	}
	if !slices.Equal(got, want) {
		t.Errorf("kept\n%v\nwant\n%v", got, want)
	}
	if k := keep(names, 0, 0, 0); len(k) != 1 || !k[names[len(names)-1]] {
		t.Errorf("with all zero, kept %v; the newest must always stay", k)
	}
}

func TestPruneKeepsLinkedFiles(t *testing.T) {
	root := t.TempDir()
	f := sampleLibrary()
	for d := 1; d <= 3; d++ {
		runFake(t, f, root, nil, day(d))
	}
	p := defaultProfile()
	p.KeepDaily, p.KeepWeekly, p.KeepMonthly = 1, 0, 0
	var out strings.Builder
	if err := prune(root, p, true, &out); err != nil || strings.Count(out.String(), "would delete") != 2 {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if len(backups(filepath.Join(root, "group-1-Test_group"))) != 3 {
		t.Fatal("dry run deleted something")
	}
	if err := prune(root, p, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	left := backups(filepath.Join(root, "group-1-Test_group"))
	if len(left) != 1 || left[0] != "2026-11-03T060000Z" {
		t.Fatalf("left %v", left)
	}
	if b, err := os.ReadFile(filepath.Join(root, "group-1-Test_group", left[0], "files", "PDF00001", "Report_ draft_.pdf")); err != nil || string(b) != "pdf-one" {
		t.Errorf("linked file after prune: %q %v", b, err)
	}
}

func TestLibraryFolderFollowsRename(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "group-1-Old name", "2026-11-01T060000Z"), 0o755)
	os.MkdirAll(filepath.Join(root, "group-12-Other"), 0o755)
	got, err := libraryFolder(root, library{ID: "group-1", Name: "New: name"})
	if err != nil || filepath.Base(got) != "group-1-New__name" {
		t.Fatalf("folder %q, err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(got, "2026-11-01T060000Z")); err != nil {
		t.Error("old backups did not move with the rename")
	}
	if _, err := os.Stat(filepath.Join(root, "group-12-Other")); err != nil {
		t.Error("another library's folder was touched")
	}
	os.MkdirAll(filepath.Join(root, "group-1"), 0o755) // old style without a name, next to the new one
	if _, err := libraryFolder(root, library{ID: "group-1", Name: "New: name"}); err == nil {
		t.Error("two folders for one library not reported")
	}
}
