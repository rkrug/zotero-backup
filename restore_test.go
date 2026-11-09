package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// snapshotOf copies the fake library's state for later comparison.
func snapshotOf(f *fakeLibrary) (map[string]map[string]any, map[string]map[string]any, map[string]string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return jsonRound(f.items), jsonRound(f.collections), maps.Clone(f.files), f.settings
}

// jsonRound gives objects the types JSON decoding gives (float64, []any, …).
func jsonRound(m map[string]map[string]any) map[string]map[string]any {
	b, _ := json.Marshal(m)
	out := map[string]map[string]any{}
	json.Unmarshal(b, &out)
	return out
}

func sameJSON(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && fmt.Sprint(x) == fmt.Sprint(y)
}

// fixed gives the same client for every library.
func fixed(c *Client) func(string) (*Client, error) {
	return func(string) (*Client, error) { return c, nil }
}

func fakeClient(t *testing.T, f *fakeLibrary) *Client {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, Key: "k", HTTP: srv.Client(), Sleep: func(time.Duration) {}}
}

// sameState compares a restored library with the original, ignoring versions.
func sameState(t *testing.T, f *fakeLibrary, items, cols map[string]map[string]any, files map[string]string) {
	t.Helper()
	got := jsonRound(f.items)
	for k, want := range items {
		got := got[k]
		if got == nil {
			t.Errorf("item %s not restored", k)
			continue
		}
		if fields := differingFields(want, got); len(fields) > 0 {
			t.Errorf("item %s differs in %v: %v vs %v", k, fields, want, got)
		}
		if want["md5"] != got["md5"] {
			t.Errorf("item %s md5 %v, want %v", k, got["md5"], want["md5"])
		}
	}
	for k := range f.items {
		if items[k] == nil {
			t.Errorf("unexpected item %s", k)
		}
	}
	for k, want := range cols {
		if f.collections[k] == nil || len(differingFields(want, f.collections[k])) > 0 {
			t.Errorf("collection %s: %v, want %v", k, f.collections[k], want)
		}
	}
	for k, want := range files {
		if f.files[k] != want {
			t.Errorf("file %s = %q, want %q", k, f.files[k], want)
		}
	}
}

// damage deletes a whole family (book, PDF, annotation, note), a collection
// and a file, changes and trashes items and changes the settings.
func damage(f *fakeLibrary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range []string{"BOOK0001", "PDF00001", "NOFILE01", "ANNO0001", "NOTE0001"} {
		delete(f.items, k)
	}
	delete(f.files, "PDF00001")
	delete(f.fulltext, "PDF00001")
	delete(f.collections, "COLL0001")
	f.items["OTHER001"]["title"] = "changed title"
	f.items["OTHER001"]["extra"] = "added on the server"
	f.items["OTHER002"]["deleted"] = 1.0
	f.items["TRASH001"]["deleted"] = nil
	delete(f.items["TRASH001"], "deleted")
	f.items["PDF00002"]["md5"] = md5hex("other content")
	f.files["PDF00002"] = "other content"
	f.items["NEW00001"] = map[string]any{"key": "NEW00001", "itemType": "book", "title": "added after the backup"}
	f.settings = `{"tagColors":{"value":[],"version":20}}`
	f.version += 5
}

func restoreSample(t *testing.T) (*fakeLibrary, *backupSet) {
	t.Helper()
	f := sampleLibrary()
	f.items["OTHER001"] = map[string]any{"key": "OTHER001", "itemType": "book", "title": "other"}
	f.items["OTHER002"] = map[string]any{"key": "OTHER002", "itemType": "book", "title": "will be trashed"}
	f.addFile("PDF00002", "OTHER001", "two.pdf", "pdf-two", true)
	dir, _ := runFake(t, f, t.TempDir(), nil, day(1))
	b, err := loadBackup(dir)
	if err != nil {
		t.Fatal(err)
	}
	return f, b
}

func TestRestoreRoundTrip(t *testing.T) {
	f, b := restoreSample(t)
	items, cols, files, settings := snapshotOf(f)
	ft := f.fulltext["PDF00001"]
	damage(f)

	c := fakeClient(t, f)
	var out strings.Builder
	if err := runRestore(fixed(c), b.dir, defaultProfile(), nil, false, true, &out, quietProgress()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "checked: the library matches the backup") {
		t.Errorf("output:\n%s", out.String())
	}
	items["NEW00001"] = jsonRound(f.items)["NEW00001"] // added after the backup: kept, not deleted
	sameState(t, f, items, cols, files)
	value := func(s string) string {
		var v map[string]struct{ Value any }
		json.Unmarshal([]byte(s), &v)
		return fmt.Sprint(v["tagColors"].Value)
	}
	if value(f.settings) != value(settings) {
		t.Errorf("settings %s", f.settings)
	}
	if !sameJSON(f.fulltext["PDF00001"], ft) {
		t.Errorf("full text %q, want %q", f.fulltext["PDF00001"], ft)
	}
	if f.uploads != 2 { // both files' content was gone from the storage service
		t.Errorf("uploads %d, want 2", f.uploads)
	}
	if f.items["OTHER002"]["deleted"] != nil || f.items["TRASH001"]["deleted"] == nil {
		t.Errorf("trash state not restored: OTHER002 %v, TRASH001 %v", f.items["OTHER002"]["deleted"], f.items["TRASH001"]["deleted"])
	}
	if f.items["OTHER001"]["extra"] != "" {
		t.Errorf("field added on the server not cleared: %q", f.items["OTHER001"]["extra"])
	}

	var again strings.Builder
	if err := runDiff(fixed(c), b.dir, defaultProfile(), nil, &again); err != nil || !strings.Contains(again.String(), "only in the library") ||
		strings.Contains(again.String(), "missing in the library") {
		t.Errorf("diff after restore: %v\n%s", err, again.String())
	}
}

func TestRestoreUsesFileStillOnServer(t *testing.T) {
	f, b := restoreSample(t)
	f.mu.Lock()
	delete(f.items, "PDF00002") // the attachment is gone, but the storage service still has its content
	f.files["KEEPCONT"] = "pdf-two"
	f.mu.Unlock()
	if err := runRestore(fixed(fakeClient(t, f)), b.dir, defaultProfile(), nil, false, true, io.Discard, quietProgress()); err != nil {
		t.Fatal(err)
	}
	if f.uploads != 0 || f.files["PDF00002"] != "pdf-two" {
		t.Errorf("uploads %d, file %q", f.uploads, f.files["PDF00002"])
	}
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	f, b := restoreSample(t)
	damage(f)
	before := f.version
	var out strings.Builder
	if err := runRestore(fixed(fakeClient(t, f)), b.dir, defaultProfile(), nil, true, false, &out, quietProgress()); err != nil {
		t.Fatal(err)
	}
	if f.version != before || f.uploads != 0 {
		t.Error("dry run wrote")
	}
	for _, want := range []string{`library  group-1 "Test group"`, "missing in the library", "BOOK0001", "different in the library", "OTHER001", "[extra, title]",
		"files to upload: 2", "settings to set back: tagColors", "only in the library", "NEW00001"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRestoreSelectedItems(t *testing.T) {
	f, b := restoreSample(t)
	damage(f)
	if err := runRestore(fixed(fakeClient(t, f)), b.dir, defaultProfile(), []string{"PDF00001"}, false, true, io.Discard, quietProgress()); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"PDF00001", "ANNO0001", "BOOK0001"} { // the item, its annotation, its parent
		if f.items[k] == nil {
			t.Errorf("%s not restored", k)
		}
	}
	if f.collections["COLL0001"] == nil {
		t.Error("collection the parent belongs to not restored")
	}
	if f.items["NOTE0001"] != nil || f.items["OTHER001"]["title"] != "changed title" || strings.Contains(f.settings, "#FF6666") {
		t.Error("restored more than the selection")
	}
}

func TestRestoreRefusesReadOnlyKey(t *testing.T) {
	f, b := restoreSample(t)
	f.readOnly = true
	err := runRestore(fixed(fakeClient(t, f)), b.dir, defaultProfile(), nil, false, true, io.Discard, quietProgress())
	if err == nil || !strings.Contains(err.Error(), "may not write") {
		t.Errorf("err = %v", err)
	}
}

func TestRestoreReportsConflicts(t *testing.T) {
	f, b := restoreSample(t)
	damage(f)
	c := fakeClient(t, f)
	now, _ := readLibrary(c, b.libPath)
	pl := makePlan(b, now, nil, nil)
	f.mu.Lock()
	f.items["OTHER001"]["version"] = 999.0 // changed by someone after the plan was made
	f.mu.Unlock()
	var out strings.Builder
	failures, err := restore(c, b, now, pl, nil, &out, quietProgress())
	if err != nil || failures != 1 || !strings.Contains(out.String(), "FAILED items OTHER001") {
		t.Errorf("failures %d, err %v\n%s", failures, err, out.String())
	}
}

// Zotero desktop uploads text files and snapshots as a ZIP, and the server
// serves that ZIP: the backup keeps it as <name>.zip, restore uploads it as a ZIP.
func TestZippedFileBackupAndRestore(t *testing.T) {
	f := sampleLibrary()
	f.addZippedFile("TEXT0001", "BOOK0001", "notes.txt", "some text")
	zipped := f.files["TEXT0001"]
	dir, _ := runFake(t, f, t.TempDir(), nil, day(1))
	var files map[string]FileEntry
	readJSON(filepath.Join(dir, "files.json"), &files)
	var m Meta
	readJSON(filepath.Join(dir, "meta.json"), &m)
	e := files["TEXT0001"]
	if !e.Zip || e.MD5 != md5hex("some text") || e.Path != "files/TEXT0001/notes.txt.zip" || len(m.Problems) > 0 {
		t.Fatalf("entry %+v, problems %v", e, m.Problems)
	}
	dir2, _ := runFake(t, f, filepath.Dir(filepath.Dir(dir)), nil, day(2))
	var again map[string]FileEntry
	readJSON(filepath.Join(dir2, "files.json"), &again)
	if a := again["TEXT0001"]; a.Source != "linked" || !a.Zip || a.Path != e.Path {
		t.Errorf("second backup: %+v", a)
	}

	b, err := loadBackup(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.items, "TEXT0001")
	delete(f.files, "TEXT0001")
	f.mu.Unlock()
	if err := runRestore(fixed(fakeClient(t, f)), b.dir, defaultProfile(), nil, false, true, io.Discard, quietProgress()); err != nil {
		t.Fatal(err)
	}
	if f.items["TEXT0001"]["md5"] != md5hex("some text") || f.files["TEXT0001"] != zipped {
		t.Errorf("restored md5 %v, ZIP kept: %v", f.items["TEXT0001"]["md5"], f.files["TEXT0001"] == zipped)
	}
}

// Older backups kept the ZIP under the file's own name with the ZIP's md5.
func TestOldBackupZipRecognised(t *testing.T) {
	f := sampleLibrary()
	f.addZippedFile("TEXT0001", "BOOK0001", "notes.txt", "some text")
	dir, _ := runFake(t, f, t.TempDir(), nil, day(1))
	var files map[string]FileEntry
	readJSON(filepath.Join(dir, "files.json"), &files)
	old := filepath.Join(dir, "files", "TEXT0001", "notes.txt")
	os.Rename(old+".zip", old)
	files["TEXT0001"] = FileEntry{Filename: "notes.txt", Path: "files/TEXT0001/notes.txt", MD5: md5hex(f.files["TEXT0001"]), Source: "downloaded"}
	writeJSON(filepath.Join(dir, "files.json"), files)
	b, err := loadBackup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := b.files["TEXT0001"]; !e.Zip || e.MD5 != md5hex("some text") {
		t.Errorf("%+v", e)
	}
}

func TestDifferingFieldsIgnoresOrderAndEmpty(t *testing.T) {
	desktop := map[string]any{"annotationAuthorName": "", "collections": []any{"B", "A"},
		"tags": []any{map[string]any{"tag": "y"}, map[string]any{"tag": "x"}}}
	server := map[string]any{"collections": []any{"A", "B"}, "tags": []any{map[string]any{"tag": "x"}, map[string]any{"tag": "y"}}}
	if d := differingFields(desktop, server); len(d) > 0 {
		t.Errorf("differ in %v", d)
	}
	server["collections"] = []any{"A"}
	if d := differingFields(desktop, server); !slices.Equal(d, []string{"collections"}) {
		t.Errorf("differ in %v, want [collections]", d)
	}
}
