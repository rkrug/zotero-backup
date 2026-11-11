package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verifySample: two backups of the sample library (with a zipped snapshot);
// the second links the files of the first.
func verifySample(t *testing.T) (root, b1, b2 string) {
	t.Helper()
	f := sampleLibrary()
	f.addZippedFile("SNAP0001", "BOOK0001", "page.html", "<html>snapshot</html>")
	root = t.TempDir()
	b1, _ = runFake(t, f, root, nil, day(1))
	f.version++
	b2, _ = runFake(t, f, root, nil, day(2))
	return root, b1, b2
}

func verifyOut(t *testing.T, root, target string, quick bool) (string, error) {
	var out strings.Builder
	err := runVerify(root, target, quick, &out)
	return out.String(), err
}

func TestVerifyGoodBackups(t *testing.T) {
	root, b1, _ := verifySample(t)
	for _, target := range []string{"", "all", b1, filepath.Dir(b1)} {
		out, err := verifyOut(t, root, target, false)
		if err != nil || !strings.Contains(out, "no problems") || strings.Contains(out, "PROBLEMS") {
			t.Errorf("verify %q: %v\n%s", target, err, out)
		}
	}
	var m Meta
	readJSON(filepath.Join(b1, "meta.json"), &m)
	if len(m.Checksums) != len(backupJSON) {
		t.Errorf("checksums %v", m.Checksums)
	}
}

func TestVerifyReadsHardLinkedFilesOnce(t *testing.T) {
	root, _, _ := verifySample(t)
	v := &verifier{seen: map[string][]os.FileInfo{}}
	lib := filepath.Join(root, "group-1-Test_group")
	for _, n := range backups(lib) {
		if r := v.check(filepath.Join(lib, n)); len(r.problems) > 0 {
			t.Fatal(r.problems)
		}
	}
	var fe map[string]FileEntry
	readJSON(filepath.Join(lib, backups(lib)[0], "files.json"), &fe)
	files := len(fe)
	if v.hashed != files {
		t.Errorf("read %d files for 2 backups of %d hard-linked files", v.hashed, files)
	}
}

func TestVerifyFindsDamage(t *testing.T) {
	cases := []struct {
		name   string
		damage func(t *testing.T, b string)
		quick  bool // also found with --quick
		want   string
	}{
		{"changed file, same size", func(t *testing.T, b string) { rewrite(t, b, "PDF00001", func(s string) string { return "PDF-ONE" }) }, false, "md5"},
		{"changed file, other size", func(t *testing.T, b string) { rewrite(t, b, "PDF00001", func(s string) string { return s + "x" }) }, true, "bytes, recorded"},
		{"missing file", func(t *testing.T, b string) { os.Remove(filePath(t, b, "PDF00001")) }, true, "missing from the backup"},
		{"broken ZIP", func(t *testing.T, b string) { rewrite(t, b, "SNAP0001", func(s string) string { return "not a zip" }) }, false, "SNAP0001"},
		{"truncated JSON", func(t *testing.T, b string) { truncate(t, filepath.Join(b, "items.json")) }, true, "items.json"},
		{"JSON changed but valid", func(t *testing.T, b string) { edit(t, filepath.Join(b, "items.json"), `"A book"`, `"A boot"`) }, true, "items.json: changed since the backup"},
		{"count off", func(t *testing.T, b string) {
			var m Meta
			readJSON(filepath.Join(b, "meta.json"), &m)
			m.Counts["book"]++
			writeJSON(filepath.Join(b, "meta.json"), m)
		}, true, "book: "},
		{"attachment file not backed up", func(t *testing.T, b string) {
			var fe map[string]FileEntry
			readJSON(filepath.Join(b, "files.json"), &fe)
			delete(fe, "PDF00001")
			writeJSON(filepath.Join(b, "files.json"), fe)
		}, true, "PDF00001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, quick := range []bool{false, true} {
				root, _, b2 := verifySample(t)
				c.damage(t, b2)
				out, err := verifyOut(t, root, b2, quick)
				found := err != nil && strings.Contains(out, "PROBLEMS") && strings.Contains(out, c.want)
				if found != (!quick || c.quick) {
					t.Errorf("quick=%v: found %v (err %v)\n%s", quick, found, err, out)
				}
			}
		})
	}
}

func TestVerifyNotesPartialAndOldBackups(t *testing.T) {
	root, b1, _ := verifySample(t)
	os.Mkdir(filepath.Join(filepath.Dir(b1), "2026-11-30T060000Z.partial"), 0o755)
	var m Meta
	readJSON(filepath.Join(b1, "meta.json"), &m)
	m.Checksums = nil // as made before v0.3.1
	writeJSON(filepath.Join(b1, "meta.json"), m)
	out, err := verifyOut(t, root, "all", false)
	if err != nil || !strings.Contains(out, ".partial in the library folder") || !strings.Contains(out, "1 of 2 backups were made before v0.3.1") {
		t.Errorf("%v\n%s", err, out)
	}
}

func filePath(t *testing.T, b, key string) string {
	var fe map[string]FileEntry
	if err := readJSON(filepath.Join(b, "files.json"), &fe); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(b, filepath.FromSlash(fe[key].Path))
}

// rewrite replaces a backup file by new content (a new file: the hard-linked
// one in the other backup stays as it was).
func rewrite(t *testing.T, b, key string, change func(string) string) {
	p := filePath(t, b, key)
	old, _ := os.ReadFile(p)
	os.Remove(p)
	if err := os.WriteFile(p, []byte(change(string(old))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func truncate(t *testing.T, p string) {
	b, _ := os.ReadFile(p)
	os.WriteFile(p, b[:len(b)/2], 0o644)
}

func edit(t *testing.T, p, old, new string) {
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s lacks %q", p, old)
	}
	os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
}
