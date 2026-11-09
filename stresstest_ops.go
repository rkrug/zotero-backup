package main

// The operations each scenario applies to its own fixture family: once in the
// Zotero desktop (JavaScript through the Debug Bridge) and once directly on the
// server through the API, as another Zotero client would.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type stressOp struct {
	name    string
	desktop string                             // JS; F is the family; may return {new: key}
	api     func(a serverOps, f *family) error // the same change made on the server
}

var stressOps = []stressOp{
	{"edit-title",
		`const it = await item(F.parent); it.setField('title', it.getField('title') + ' EDITED'); await it.saveTx(); return {};`,
		func(a serverOps, f *family) error {
			d, err := a.item(f.Parent)
			if err != nil {
				return err
			}
			return a.patch("items", f.Parent, map[string]any{"title": fmt.Sprint(d["title"]) + " EDITED (api)"})
		}},
	{"fill-extra",
		`const it = await item(F.parent); it.setField('extra', 'zb-e2e extra'); await it.saveTx(); return {};`,
		func(a serverOps, f *family) error {
			return a.patch("items", f.Parent, map[string]any{"extra": "zb-e2e extra (api)"})
		}},
	{"trash-item",
		`const it = await item(F.parent); it.deleted = true; await it.saveTx(); return {};`,
		func(a serverOps, f *family) error { return a.patch("items", f.Parent, map[string]any{"deleted": 1}) }},
	{"erase-item",
		`await (await item(F.parent)).eraseTx(); return {};`,
		func(a serverOps, f *family) error { return a.delete("items", f.Parent) }},
	{"erase-note",
		`await (await item(F.note)).eraseTx(); return {};`,
		func(a serverOps, f *family) error { return a.delete("items", f.Note) }},
	{"erase-attachment",
		`await (await item(F.pdf)).eraseTx(); return {};`,
		func(a serverOps, f *family) error { return a.delete("items", f.PDF) }},
	{"erase-annotation",
		`await (await item(F.annotation)).eraseTx(); return {};`,
		func(a serverOps, f *family) error { return a.delete("items", f.Annotation) }},
	{"edit-note",
		`const n = await item(F.note); n.setNote(n.getNote() + '<p>edited</p>'); await n.saveTx(); return {};`,
		func(a serverOps, f *family) error {
			d, err := a.item(f.Note)
			if err != nil {
				return err
			}
			return a.patch("items", f.Note, map[string]any{"note": fmt.Sprint(d["note"]) + "<p>edited (api)</p>"})
		}},
	{"replace-file",
		`const it = await item(F.file); const p = await it.getFilePathAsync();
		 await IOUtils.writeUTF8(p, 'replaced ' + Math.random());
		 it.attachmentSyncState = Zotero.Sync.Storage.Local.SYNC_STATE_TO_UPLOAD; await it.saveTx({ skipAll: true }); return {};`,
		func(a serverOps, f *family) error { return a.replaceFile(f.File) }},
	{"trash-collection",
		`const c = await coll(F.coll); c.deleted = true; await c.saveTx(); return {};`,
		func(a serverOps, f *family) error {
			return a.patch("collections", f.Coll, map[string]any{"deleted": true})
		}},
	{"erase-collection",
		`await (await coll(F.coll)).eraseTx(); return {};`,
		func(a serverOps, f *family) error { return a.delete("collections", f.Coll+","+f.Sub) }},
	{"move-membership",
		`const it = await item(F.parent); it.setCollections([F.sub]); await it.saveTx(); return {};`,
		func(a serverOps, f *family) error {
			return a.patch("items", f.Parent, map[string]any{"collections": []string{f.Sub}})
		}},
	{"tags",
		`const it = await item(F.parent); it.removeTag('zb-e2e-t1'); it.addTag('zb-e2e-added'); await it.saveTx(); return {};`,
		func(a serverOps, f *family) error {
			d, err := a.item(f.Parent)
			if err != nil {
				return err
			}
			var tags []map[string]any
			for _, t := range d["tags"].([]any) {
				if m := t.(map[string]any); m["tag"] != "zb-e2e-t1" {
					tags = append(tags, m)
				}
			}
			return a.patch("items", f.Parent, map[string]any{"tags": append(tags, map[string]any{"tag": "zb-e2e-added"})})
		}},
	{"tag-colour",
		`await Zotero.Tags.setColor(L, F.colourTag, '#2EA8E5'); return {};`,
		func(a serverOps, f *family) error { return a.setTagColour(f.ColourTag, "#2EA8E5") }},
	{"new-item",
		`const it = new Zotero.Item('book'); it.libraryID = L;
		 it.setField('title', '[zb-e2e] new after backup ' + F.op); it.addTag(TAG); it.addTag('zb-e2e-run-' + query.run);
		 await it.saveTx(); return { new: it.key };`,
		func(a serverOps, f *family) error {
			k, err := a.newItem("[zb-e2e] new after backup (api) "+f.Op, f.runTag())
			f.New = k
			return err
		}},
}

func (f family) runTag() string { return "zb-e2e-run-" + f.Run }

// serverOps makes changes on the server as another Zotero client would.
type serverOps struct {
	c   *Client
	lib string // e.g. /groups/6719916
}

func (a serverOps) item(key string) (map[string]any, error) {
	var o struct {
		Data map[string]any `json:"data"`
	}
	_, err := a.c.getJSON(a.lib+"/items/"+key, &o)
	return o.Data, err
}

func (a serverOps) version(kind, key string) (any, error) {
	var o struct {
		Version float64 `json:"version"`
	}
	_, err := a.c.getJSON(a.lib+"/"+kind+"/"+key, &o)
	return o.Version, err
}

// patch writes only the given fields (a partial object), with the current version.
func (a serverOps) patch(kind, key string, fields map[string]any) error {
	v, err := a.version(kind, key)
	if err != nil {
		return err
	}
	obj := map[string]any{"key": key, "version": v}
	for k, x := range fields {
		obj[k] = x
	}
	res, err := a.c.writeObjects(a.lib, kind, []map[string]any{obj})
	if err == nil && len(res.Failed) > 0 {
		err = fmt.Errorf("%v", res.Failed)
	}
	return err
}

// delete permanently deletes objects (keys comma separated) at the current library version.
func (a serverOps) delete(kind, keys string) error {
	_, h, err := a.c.get(a.lib + "/" + kind + "?limit=1")
	if err != nil {
		return err
	}
	param := map[string]string{"items": "itemKey", "collections": "collectionKey"}[kind]
	code, _, b, err := a.c.write("DELETE", a.lib+"/"+kind+"?"+param+"="+keys, nil,
		map[string]string{"If-Unmodified-Since-Version": h.Get("Last-Modified-Version")}, false)
	if err == nil && code != http.StatusNoContent {
		err = fmt.Errorf("DELETE %s: HTTP %d %s", keys, code, b)
	}
	return err
}

func (a serverOps) setTagColour(name, colour string) error {
	body, h, err := a.c.get(a.lib + "/settings")
	if err != nil {
		return err
	}
	var s map[string]struct {
		Value []map[string]any `json:"value"`
	}
	json.Unmarshal(body, &s)
	vals := s["tagColors"].Value
	for _, v := range vals {
		if v["name"] == name {
			v["color"] = colour
		}
	}
	lv, _ := strconv.Atoi(h.Get("Last-Modified-Version"))
	return a.c.writeSettings(a.lib, map[string]any{"tagColors": map[string]any{"value": vals}}, lv)
}

func (a serverOps) newItem(title, runTag string) (string, error) {
	res, err := a.c.writeObjects(a.lib, "items", []map[string]any{{
		"itemType": "book", "title": title,
		"tags": []map[string]any{{"tag": "zb-e2e"}, {"tag": runTag}},
	}})
	if err != nil {
		return "", err
	}
	if len(res.Written) != 1 {
		return "", fmt.Errorf("new item not written: %v", res.Failed)
	}
	return res.Written[0], nil
}

// replaceFile uploads new content for an attachment (If-Match on its md5).
func (a serverOps) replaceFile(key string) error {
	d, err := a.item(key)
	if err != nil {
		return err
	}
	p := filepath.Join(os.TempDir(), "zb-e2e-"+key+".txt")
	content := fmt.Sprintf("replaced through the API %d", os.Getpid())
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return err
	}
	defer os.Remove(p)
	m, _, _ := md5File(p)
	_, err = a.c.uploadFile(a.lib, key, p, fmt.Sprint(d["filename"]), m, 1_700_000_000_000, fmt.Sprint(d["md5"]), false)
	return err
}
