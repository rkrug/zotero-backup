package main

import (
	"archive/zip"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// zipChecks (part of the extras): an HTML attachment with a second file in its
// folder, which Zotero desktop uploads as a ZIP; a restore that really uploads a
// ZIP the server has never seen (anything in a real backup came from the server,
// which then only answers "exists"); and the form older backups kept ZIPs in.
func (s *stress) zipChecks() {
	const sc, op = "extras", "zip"
	var fx struct{ Parent, Snapshot, Filename string }
	if err := s.z.run(jsMakeSnapshot, map[string]string{"run": s.run + "-zip"}, &fx); err != nil {
		s.record(sc, op, "snapshot with two files in Zotero desktop", false, "%v", err)
		return
	}
	k := fx.Snapshot
	s.sync(sc, "sync the snapshot", "fail")
	b, err := s.backup()
	if err != nil {
		s.record(sc, op, "backup", false, "%v", err)
		return
	}
	e := b.files[k]
	inside, _ := zipContents(filepath.Join(b.dir, filepath.FromSlash(e.Path)))
	s.record(sc, op, "backup keeps Zotero's ZIP (<name>.zip, md5 of the page, both files inside)",
		e.Zip && e.MD5 == b.items[k]["md5"] && strings.HasSuffix(e.Path, ".zip") && len(inside) == 2 && inside[fx.Filename] != "",
		"%+v, inside %v", e, slices.Collect(maps.Keys(inside)))

	// a changed page: restore must upload it (the server never had this ZIP)
	page := fmt.Sprintf(`<html><head><link rel="stylesheet" href="zb-e2e-extra.css"></head><body><p>[zb-e2e] %s, put back by restore %d</p></body></html>`, s.run, time.Now().UnixNano())
	up := filepath.Join(s.root, "zip", "upload")
	pageMD5, zipMD5, err := zipVariant(b, up, k, []byte(page), false)
	if err != nil {
		s.record(sc, op, "backup with a changed page", false, "%v", err)
		return
	}
	s.restoreZip(op, up, k, fx.Filename, "restore uploads a ZIP the server never had")
	want, _ := zipContents(filepath.Join(up, filepath.FromSlash(e.Path)))
	b2 := s.checkServedZip(op, k, pageMD5, zipMD5, "next backup: exactly that ZIP from the server")
	s.sync(sc, "sync the uploaded ZIP", "fail")
	var got map[string]string
	err = s.z.run(jsStorageFiles, map[string]string{"key": k}, &got)
	s.record(sc, op, "Zotero desktop unpacks both files", err == nil && maps.Equal(got, want), "%v: desktop %v, ZIP %v", err, got, want)
	if b2 == nil {
		return
	}

	// older backups: the ZIP under the file's own name, with the ZIP's md5, no "zip"
	same := filepath.Join(s.root, "zip", "old-form-same")
	if _, _, err := zipVariant(b2, same, k, []byte(page), true); err == nil {
		var fe FileEntry
		ob, err := loadBackup(same)
		now, err2 := readLibrary(s.c, s.lib.Path)
		ok := err == nil && err2 == nil
		if ok {
			fe = ob.files[k]
			ok = fe.Zip && fe.MD5 == pageMD5 && len(makePlan(ob, now, nil, []string{k}).files) == 0
		}
		s.record(sc, op, "older backup: ZIP recognised, no file difference to the library", ok, "%v %v %+v", err, err2, fe)
	} else {
		s.record(sc, op, "older backup", false, "%v", err)
	}
	changed := filepath.Join(s.root, "zip", "old-form-changed")
	pageMD5, zipMD5, err = zipVariant(b2, changed, k, []byte(page+"\n<!-- older backup -->"), true)
	if err != nil {
		s.record(sc, op, "older backup with a changed page", false, "%v", err)
		return
	}
	s.restoreZip(op, changed, k, fx.Filename, "older backup: restore uploads the ZIP as a ZIP")
	s.checkServedZip(op, k, pageMD5, zipMD5, "older backup: next backup has the page's md5 and that ZIP")
}

func (s *stress) restoreZip(op, dir, k, filename, check string) {
	var out strings.Builder
	err := runRestore(fixedClient(s.c), dir, s.profile, []string{k}, "", false, true, &out, quietProgress())
	ok := err == nil && strings.Contains(out.String(), "file "+k+" "+filename+": uploaded\n")
	detail := ""
	if !ok {
		detail = fmt.Sprintf("%v\n%s", err, out.String())
	}
	s.record("extras", op, check, ok, "%s", detail)
}

// checkServedZip backs up and checks that the server has the page's md5 and
// serves the ZIP with zipMD5.
func (s *stress) checkServedZip(op, k, pageMD5, zipMD5, check string) *backupSet {
	b, err := s.backup()
	if err != nil {
		s.record("extras", op, check, false, "%v", err)
		return nil
	}
	e := b.files[k]
	got, _, _ := md5File(filepath.Join(b.dir, filepath.FromSlash(e.Path)))
	s.record("extras", op, check, e.Zip && e.MD5 == pageMD5 && b.items[k]["md5"] == pageMD5 && got == zipMD5,
		"%+v, item md5 %v, ZIP md5 %s (want page %s, ZIP %s)", e, b.items[k]["md5"], got, pageMD5, zipMD5)
	return b
}

// zipContents maps each file in a ZIP to its md5.
func zipContents(path string) (map[string]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := md5.New()
		_, err = io.Copy(h, rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out[f.Name] = hex.EncodeToString(h.Sum(nil))
	}
	return out, nil
}

// zipVariant copies backup b to dir (files hard-linked, the JSON files copied)
// with the ZIP of attachment k rebuilt around page; old writes it as older
// backups did (under the file's own name, the ZIP's md5, no "zip"). It returns
// the md5 of the page and of the new ZIP.
func zipVariant(b *backupSet, dir, k string, page []byte, old bool) (pageMD5, zipMD5 string, err error) {
	e, ok := b.files[k]
	if !ok || !e.Zip {
		return "", "", fmt.Errorf("%s is no ZIP in %s", k, b.dir)
	}
	os.RemoveAll(dir)
	err = filepath.WalkDir(b.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(b.dir, p)
		to := filepath.Join(dir, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		case strings.HasSuffix(p, ".json"): // rewritten below: never through a hard link
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(to, data, 0o644)
		}
		return linkOrCopy(p, to)
	})
	if err != nil {
		return "", "", err
	}
	src := filepath.Join(b.dir, filepath.FromSlash(e.Path))
	r, err := zip.OpenReader(src)
	if err != nil {
		return "", "", err
	}
	defer r.Close()
	os.Remove(filepath.Join(dir, filepath.FromSlash(e.Path)))
	if old {
		e.Path = strings.TrimSuffix(e.Path, ".zip")
	}
	dst := filepath.Join(dir, filepath.FromSlash(e.Path))
	f, err := os.Create(dst)
	if err != nil {
		return "", "", err
	}
	w := zip.NewWriter(f)
	for _, zf := range r.File {
		var data []byte
		if zf.Name == e.Filename {
			data = page
		} else {
			rc, err := zf.Open()
			if err != nil {
				return "", "", err
			}
			data, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", "", err
			}
		}
		zw, err := w.Create(zf.Name)
		if err != nil {
			return "", "", err
		}
		zw.Write(data)
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}
	sum := md5.Sum(page)
	pageMD5 = hex.EncodeToString(sum[:])
	zipMD5, size, err := md5File(dst)
	if err != nil {
		return "", "", err
	}
	e.MD5, e.Size, e.Zip, e.Source = pageMD5, size, !old, "test"
	if old {
		e.MD5 = zipMD5
	}
	var files map[string]FileEntry
	var items []map[string]any
	if err := readJSON(filepath.Join(dir, "files.json"), &files); err != nil {
		return "", "", err
	}
	if err := readJSON(filepath.Join(dir, "items.json"), &items); err != nil {
		return "", "", err
	}
	files[k] = e
	for _, it := range items {
		if it["key"] == k {
			it["md5"], it["mtime"] = pageMD5, time.Now().UnixMilli()
		}
	}
	if err := writeJSON(filepath.Join(dir, "files.json"), files); err != nil {
		return "", "", err
	}
	return pageMD5, zipMD5, writeJSON(filepath.Join(dir, "items.json"), items)
}

// jsMakeSnapshot creates a webpage item with an HTML attachment and a second
// file (a stylesheet) in the attachment's folder; Zotero uploads both as a ZIP.
const jsMakeSnapshot = `
const run = query.run;
const dir = PathUtils.join(PathUtils.tempDir, 'zb-e2e');
await IOUtils.makeDirectory(dir, { ignoreExisting: true });
const p = new Zotero.Item('webpage');
p.libraryID = L;
p.setField('title', '[zb-e2e] ' + run + ' snapshot with two files');
p.addTag(TAG); p.addTag('zb-e2e-run-' + run);
await p.saveTx();
const html = PathUtils.join(dir, run + '-snapshot.html');
await IOUtils.writeUTF8(html, '<html><head><link rel="stylesheet" href="zb-e2e-extra.css"></head><body><p>[zb-e2e] ' + run + ' ' + Math.random() + '</p></body></html>');
const a = await Zotero.Attachments.importFromFile({ file: html, parentItemID: p.id, contentType: 'text/html' });
await IOUtils.writeUTF8(PathUtils.join(Zotero.Attachments.getStorageDirectory(a).path, 'zb-e2e-extra.css'), 'p { color: #c00 }');
return { parent: p.key, snapshot: a.key, filename: a.attachmentFilename };
`

// jsStorageFiles returns the files in a fixture attachment's folder (md5 by
// name), after downloading what the server has newer.
const jsStorageFiles = `
const it = await item(query.key);
const stale = it.attachmentSyncState == Zotero.Sync.Storage.Local.SYNC_STATE_TO_DOWNLOAD;
if (stale || !(await it.fileExists())) { try { await Zotero.Sync.Runner.downloadFile(it); } catch (e) {} }
const out = {};
for (const p of await IOUtils.getChildren(Zotero.Attachments.getStorageDirectory(it).path)) {
  const n = PathUtils.filename(p);
  if (!n.startsWith('.')) out[n] = await Zotero.Utilities.Internal.md5Async(p);
}
return out;
`
