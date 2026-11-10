package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoBackups: day 1 the sample library (with a note text and a zipped
// snapshot), day 2 without the note and with the book's title changed.
func twoBackups(t *testing.T) (root string, f *fakeLibrary) {
	t.Helper()
	f = sampleLibrary()
	f.items["NOTE0001"]["note"] = "<p>Remember the <b>Kruger</b> data</p>"
	f.items["BOOK0001"]["creators"] = []any{map[string]any{"lastName": "Smith", "firstName": "Anna", "creatorType": "author"}}
	f.addZippedFile("SNAP0001", "BOOK0001", "page.html", "<html>snapshot</html>")
	root = t.TempDir()
	runFake(t, f, root, nil, day(1))
	delete(f.items, "NOTE0001")
	f.items["BOOK0001"]["title"] = "A book, second edition"
	f.version++
	runFake(t, f, root, nil, day(2))
	return root, f
}

func TestFind(t *testing.T) {
	root, _ := twoBackups(t)
	cases := []struct{ text, want, not string }{
		{"kruger", "NOTE0001  note", "BOOK0001  book"},                   // note text, without the HTML
		{"KRUGER", "NOT in the newest backup", ""},                       // case-insensitive; gone on day 2
		{"smith", "in 2 of 2 backups", "NOT in the newest"},              // creator
		{"second edition", "A book, second edition", ""},                 // the newest title
		{"draft?.pdf", "under BOOK0001 A book, second edition", ""},      // file name, with its parent
		{"TRASH001", "in the trash in the newest backup", ""},            // key
		{"no such thing", `No item matches "no such thing".`, "group-1"}, // nothing
	}
	for _, c := range cases {
		var out strings.Builder
		if err := runFind(root, "", c.text, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), c.want) || (c.not != "" && strings.Contains(out.String(), c.not)) {
			t.Errorf("find %q: want %q, not %q:\n%s", c.text, c.want, c.not, out.String())
		}
	}
	lib := filepath.Join(root, "group-1-Test_group")
	var out strings.Builder
	if err := runFind(root, lib, "smith", &out); err != nil || !strings.Contains(out.String(), "BOOK0001") {
		t.Errorf("one library folder: %v\n%s", err, out.String())
	}
	if err := runFind(root, root, "smith", &out); err == nil {
		t.Error("a folder without backups accepted as a library folder")
	}
}

func TestShowAndSave(t *testing.T) {
	root, _ := twoBackups(t)
	first := filepath.Join(root, "group-1-Test_group", backups(filepath.Join(root, "group-1-Test_group"))[0])
	var out strings.Builder
	if err := runShow(first, "BOOK0001", "", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BOOK0001  book", "title                A book", "creators             Smith, Anna", "collection           COLL0001 C",
		"PDF00001  attachment", "ANNO0001  annotation", "NOTE0001  note", "note                 Remember the Kruger data", "file in the backup"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, out.String())
		}
	}
	if err := runShow(first, "NOPE0001", "", &out); err == nil || !strings.Contains(err.Error(), "zotero-backup find") {
		t.Errorf("unknown key: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "out")
	out.Reset()
	if err := runShow(first, "BOOK0001", dir, &out); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "PDF00001", safeName(`Report: draft?.pdf`, "PDF00001"))); err != nil || string(b) != "pdf-one" {
		t.Errorf("PDF not saved: %q %v\n%s", b, err, out.String())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "SNAP0001", "page.html")); err != nil || string(b) != "<html>snapshot</html>" {
		t.Errorf("snapshot not unpacked: %q %v", b, err)
	}
	if err := runShow(first, "BOOK0001", dir, &out); err == nil { // never overwrites
		t.Error("saved over existing files")
	}
	if err := runShow(first, "BOOK0001", filepath.Join(root, "x"), &out); err == nil || !strings.Contains(err.Error(), "inside the backups") {
		t.Errorf("save into the backups: %v", err)
	}
}

func TestSaveFileKeepsZipEntriesInside(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.zip")
	os.WriteFile(src, zipFileOf(t, "../../evil.txt", []byte("x")), 0o644)
	out := filepath.Join(dir, "out")
	os.Mkdir(out, 0o755)
	names, err := saveFile(src, out, "a", true)
	if err != nil || len(names) != 1 || names[0] != "evil.txt" {
		t.Fatalf("%v %v", names, err)
	}
	if _, err := os.Stat(filepath.Join(out, "evil.txt")); err != nil {
		t.Error(err)
	}
}

func TestDiffBackups(t *testing.T) {
	f := sampleLibrary()
	f.items["NOTE0001"]["note"] = "<p>note</p>"
	root := t.TempDir()
	d1, _ := runFake(t, f, root, nil, day(1))
	delete(f.items, "NOTE0001")
	f.items["BOOK0001"]["title"] = "A book, second edition"
	f.items["NEW00001"] = map[string]any{"key": "NEW00001", "itemType": "book", "title": "new"}
	f.items["PDF00001"]["md5"] = md5hex("pdf-one, changed")
	f.files["PDF00001"] = "pdf-one, changed"
	f.settings = `{"tagColors":{"value":[],"version":20}}`
	f.version++
	d2, _ := runFake(t, f, root, nil, day(2))
	d3, _ := runFake(t, f, root, []string{"notes"}, day(3))

	var out strings.Builder
	if err := runDiffBackups(d2, d1, defaultProfile(), nil, &out); err != nil { // either order
		t.Fatal(err)
	}
	for _, want := range []string{"older    " + d1, "newer    " + d2, "added (only in the newer backup): 1", "NEW00001",
		"removed (only in the older backup): 1", "NOTE0001", "changed: ", "BOOK0001  book             A book  [title]",
		"files changed: 1", "PDF00001", "settings changed: tagColors"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := runDiffBackups(d2, d3, defaultProfile(), nil, &out); err != nil || !strings.Contains(out.String(), "No differences.") {
		t.Errorf("d2 vs d3 (d3 without notes, none left anyway): %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runDiffBackups(d1, d3, defaultProfile(), nil, &out); err != nil || strings.Contains(out.String(), "NOTE0001") {
		t.Errorf("a note is not \"removed\" when the newer backup left notes out: %v\n%s", err, out.String())
	}

	other := sampleLibrary()
	dOther, _ := runFake(t, other, t.TempDir(), nil, day(1))
	var m Meta
	readJSON(filepath.Join(dOther, "meta.json"), &m)
	m.Library = "group-2"
	writeJSON(filepath.Join(dOther, "meta.json"), m)
	if err := runDiffBackups(d1, dOther, defaultProfile(), nil, &out); err == nil || !strings.Contains(err.Error(), "different libraries") {
		t.Errorf("different libraries: %v", err)
	}
}
