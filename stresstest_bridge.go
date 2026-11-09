package main

// The Zotero desktop side of `zotero-backup stresstest`: JavaScript run inside the
// running Zotero through the Zotero Debug Bridge plugin
// (POST http://127.0.0.1:23119/debug-bridge/execute, Bearer token).
//
// Every script starts with a prelude that pins it to ONE library (a group, by
// ID and name, or the personal library, by user ID and name) and refuses to
// touch anything that is not a test fixture: items whose top-level
// parent carries the tag "zb-e2e", collections whose name starts with "[zb-e2e]".

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type bridge struct {
	endpoint string
	token    string
	kind     string // "group" or "user"
	id       int    // group ID, or the user ID of the personal library
	name     string // group name, or the username
	http     *http.Client
}

// bridgeToken reads the Debug Bridge token from the Zotero profile's prefs.js
// (never printed).
func bridgeToken(profileDir string) (string, error) {
	if t := os.Getenv("ZOTERO_DEBUG_BRIDGE_TOKEN"); t != "" {
		return t, nil
	}
	b, err := os.ReadFile(profileDir + "/prefs.js")
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`user_pref\("extensions\.zotero\.debug-bridge\.token",\s*"([^"]+)"\);`).FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("no debug-bridge token in %s/prefs.js", profileDir)
	}
	return string(m[1]), nil
}

// run executes the prelude plus js in Zotero with the given query values and
// decodes the script's return value into out.
func (b *bridge) run(js string, query map[string]string, out any) error {
	q := url.Values{"lib": {b.kind}, "id": {strconv.Itoa(b.id)}, "name": {b.name}}
	for k, v := range query {
		q.Set(k, v)
	}
	req, err := http.NewRequest("POST", b.endpoint+"?"+q.Encode(), bytes.NewReader([]byte(jsPrelude+"\n"+js)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/javascript")
	req.Header.Set("Authorization", "Bearer "+b.token)
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("Zotero Debug Bridge: %w (is Zotero running, with the Debug Bridge plugin?)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("Zotero Debug Bridge: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func newBridge(profileDir, kind string, id int, name string) (*bridge, error) {
	tok, err := bridgeToken(profileDir)
	if err != nil {
		return nil, err
	}
	ep := os.Getenv("ZOTERO_DEBUG_BRIDGE_URL")
	if ep == "" {
		ep = "http://127.0.0.1:23119/debug-bridge/execute"
	}
	return &bridge{ep, tok, kind, id, name, &http.Client{Timeout: 20 * time.Minute}}, nil
}

// family is one fixture: a book with a note, a text attachment, a PDF with an
// annotation, in a collection and its subcollection, with tags and a coloured tag.
type family struct {
	Run        string `json:"run"`
	Op         string `json:"op"`
	Parent     string `json:"parent"`
	Note       string `json:"note"`
	File       string `json:"file"`
	PDF        string `json:"pdf"`
	Annotation string `json:"annotation"`
	Coll       string `json:"coll"`
	Sub        string `json:"sub"`
	ColourTag  string `json:"colourTag"`
	New        string `json:"new,omitempty"` // item created by the op "new-item"
}

func (f family) itemKeys() []string {
	k := []string{f.Parent, f.Note, f.File, f.PDF, f.Annotation}
	if f.New != "" {
		k = append(k, f.New)
	}
	return k
}

func (f family) json() string { b, _ := json.Marshal(f); return string(b) }

// syncResult is what a desktop sync reports.
type syncResult struct {
	Conflicts []struct {
		Key    string `json:"key"`
		Type   string `json:"type"`
		Local  string `json:"local"`  // changed / deleted
		Remote string `json:"remote"` // changed / deleted
	} `json:"conflicts"`
	UnsyncedItems       int `json:"unsyncedItems"`
	UnsyncedCollections int `json:"unsyncedCollections"`
	LibraryVersion      int `json:"libraryVersion"`
}

// sync syncs only the test library. policy decides conflicts like the user in
// Zotero's conflict window would: "local", "remote", or "fail" (cancel the
// window; the test then reports the conflict as unexpected).
func (b *bridge) sync(policy string) (syncResult, error) {
	var r syncResult
	err := b.run(jsSync, map[string]string{"policy": policy}, &r)
	return r, err
}

// localObject is an object as the desktop has it.
type localObject struct {
	Exists  bool           `json:"exists"`
	Deleted bool           `json:"deleted"`
	JSON    map[string]any `json:"json"`
	FileMD5 string         `json:"fileMD5"`
}

func (b *bridge) state(f family) (map[string]localObject, error) {
	var out map[string]localObject
	err := b.run(jsState, map[string]string{"family": f.json()}, &out)
	return out, err
}

// The prelude pins every script to the test library and defines the helpers.
const jsPrelude = `
let L;
if (query.lib === 'user') {
  L = Zotero.Libraries.userLibraryID;
  if (Zotero.Users.getCurrentUserID() !== Number(query.id) || Zotero.Users.getCurrentUsername() !== query.name)
    throw new Error('personal library of ' + Zotero.Users.getCurrentUsername() + ' (' + Zotero.Users.getCurrentUserID() + '), expected ' + query.name + ' (' + query.id + ')');
} else if (query.lib === 'group') {
  L = Zotero.Groups.getLibraryIDFromGroupID(Number(query.id));
  if (!L) throw new Error('group ' + query.id + ' is not in this Zotero');
  if (Zotero.Libraries.get(L).name !== query.name) throw new Error('library ' + L + ' is "' + Zotero.Libraries.get(L).name + '", expected "' + query.name + '"');
} else {
  throw new Error('no library given');
}
if (Zotero.Prefs.get('sync.autoSync')) throw new Error('automatic sync is on: switch it off for the test');
const TAG = 'zb-e2e';
const F = query.family ? JSON.parse(query.family) : null;

function topOf(it) {
  while (it && it.parentItemID) it = Zotero.Items.get(it.parentItemID);
  return it;
}
async function item(key) {
  const it = await Zotero.Items.getByLibraryAndKeyAsync(L, key);
  if (!it) throw new Error('no item ' + key);
  if (it.libraryID !== L) throw new Error('item ' + key + ' is not in the test library');
  const top = topOf(it);
  if (!top.hasTag(TAG)) throw new Error('item ' + key + ' is not a test fixture (no tag ' + TAG + ')');
  return it;
}
async function maybeItem(key) {
  const it = key && await Zotero.Items.getByLibraryAndKeyAsync(L, key);
  return it ? await item(key) : null;
}
async function coll(key) {
  const c = await Zotero.Collections.getByLibraryAndKeyAsync(L, key);
  if (!c) throw new Error('no collection ' + key);
  if (c.libraryID !== L || !c.name.startsWith('[zb-e2e]')) throw new Error('collection ' + key + ' is not a test fixture');
  return c;
}
async function md5(it) {
  const p = await it.getFilePathAsync();
  return p ? await Zotero.Utilities.Internal.md5Async(p) : '';
}
function minimalPDF(text) {
  const t = text.replace(/[()\\\\]/g, '');
  return '%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n' +
    '3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n' +
    '4 0 obj<</Length ' + (44 + t.length) + '>>stream\nBT /F1 12 Tf 72 720 Td (' + t + ') Tj ET\nendstream endobj\n' +
    '5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n';
}
`

// jsMakeFamilies creates one fixture family per op (query.ops, comma separated)
// for run query.run and returns them.
const jsMakeFamilies = `
const run = query.run;
const dir = PathUtils.join(PathUtils.tempDir, 'zb-e2e');
await IOUtils.makeDirectory(dir, { ignoreExisting: true });
const out = [];
let pos = 0;
for (const op of query.ops.split(',')) {
  const name = '[zb-e2e] ' + run + ' ' + op;
  const c = new Zotero.Collection({ libraryID: L, name });
  await c.saveTx();
  const s = new Zotero.Collection({ libraryID: L, name: name + ' sub', parentID: c.id });
  await s.saveTx();
  const colourTag = 'zb-e2e-colour-' + run + '-' + op;
  const p = new Zotero.Item('book');
  p.libraryID = L;
  p.setField('title', name);
  p.setField('date', '2026');
  p.addTag(TAG); p.addTag('zb-e2e-run-' + run); p.addTag('zb-e2e-t1'); p.addTag(colourTag);
  p.setCollections([c.key, s.key]);
  await p.saveTx();
  const n = new Zotero.Item('note');
  n.libraryID = L; n.parentID = p.id; n.setNote('<p>' + name + ' note</p>');
  await n.saveTx();
  const txt = PathUtils.join(dir, run + '-' + op + '.txt');
  await IOUtils.writeUTF8(txt, name + ' ' + Math.random());
  const f = await Zotero.Attachments.importFromFile({ file: txt, parentItemID: p.id });
  const pdfPath = PathUtils.join(dir, run + '-' + op + '.pdf');
  await IOUtils.writeUTF8(pdfPath, minimalPDF(name + ' ' + Math.random()));
  const pdf = await Zotero.Attachments.importFromFile({ file: pdfPath, parentItemID: p.id, contentType: 'application/pdf' });
  const a = await Zotero.Annotations.saveFromJSON(pdf, {
    key: Zotero.DataObjectUtilities.generateKey(), type: 'highlight', text: 'zb-e2e ' + op, comment: '',
    color: '#ffd400', pageLabel: '1', sortIndex: '00000|000100|00100',
    position: { pageIndex: 0, rects: [[100, 700, 200, 712]] }, tags: []
  });
  await Zotero.Tags.setColor(L, colourTag, '#5FB236');
  out.push({ run, op, parent: p.key, note: n.key, file: f.key, pdf: pdf.key, annotation: a.key, coll: c.key, sub: s.key, colourTag });
}
return out;
`

// jsSync syncs only the test library, answering Zotero's conflict window
// automatically (query.policy: local / remote / fail), and reports what it saw.
const jsSync = `
const local = Zotero.Sync.Data.Local;
const orig = local.showConflictResolutionWindow;
const seen = [];
local.showConflictResolutionWindow = function (conflicts) {
  for (const c of conflicts) {
    seen.push({ key: (c.left && c.left.key) || (c.right && c.right.key), type: (c.left && c.left.itemType) || (c.right && c.right.itemType) || '',
      local: c.left && c.left.deleted ? 'deleted' : 'changed', remote: c.right && c.right.deleted ? 'deleted' : 'changed' });
  }
  if (query.policy === 'fail') return null; // like "Cancel": nothing is merged
  const side = query.policy === 'local' ? 'left' : 'right';
  return conflicts.map(c => {
    const data = Object.assign({}, c[side]);
    data.version = c[side].version;
    return { data, selected: side };
  });
};
try {
  await Zotero.Sync.Runner.sync({ libraries: [L] });
} finally {
  local.showConflictResolutionWindow = orig;
}
const unsyncedItems = (await Zotero.Sync.Data.Local.getUnsynced('item', L)).length;
const unsyncedCollections = (await Zotero.Sync.Data.Local.getUnsynced('collection', L)).length;
return { conflicts: seen, unsyncedItems, unsyncedCollections, libraryVersion: Zotero.Libraries.get(L).libraryVersion };
`

// jsState returns the desktop's view of a family (and the colour of its tag).
const jsState = `
const out = {};
const keys = [F.parent, F.note, F.file, F.pdf, F.annotation, F.new].filter(Boolean);
for (const k of keys) {
  const it = await Zotero.Items.getByLibraryAndKeyAsync(L, k);
  if (!it) { out[k] = { exists: false }; continue; }
  const o = { exists: true, deleted: !!it.deleted, json: it.toJSON() };
  if (it.isFileAttachment()) {
    // a file the server has newer (e.g. put back by a restore) is downloaded first
    const stale = it.attachmentSyncState == Zotero.Sync.Storage.Local.SYNC_STATE_TO_DOWNLOAD;
    if (stale || !(await it.fileExists())) { try { await Zotero.Sync.Runner.downloadFile(it); } catch (e) {} }
    o.fileMD5 = await md5(it);
  }
  out[k] = o;
}
for (const k of [F.coll, F.sub]) {
  const c = await Zotero.Collections.getByLibraryAndKeyAsync(L, k);
  out[k] = c ? { exists: true, deleted: !!c.deleted, json: c.toJSON() } : { exists: false };
}
const colour = Zotero.Tags.getColor(L, F.colourTag);
out['tagColour:' + F.colourTag] = { exists: !!colour, json: colour ? { color: colour.color } : {} };
return out;
`

// jsTouch makes a second, unsynced local change to the first surviving item of
// the family (Extra gets a line), so that a later sync collides with a restore.
const jsTouch = `
for (const k of [F.parent, F.note, F.file, F.pdf]) {
  const it = await maybeItem(k);
  if (!it || it.deleted) continue;
  if (it.isRegularItem()) {
    const e = it.getField('extra');
    it.setField('extra', (e ? e + '\n' : '') + 'touched locally');
  } else if (it.isNote()) {
    it.setNote(it.getNote() + '<p>touched locally</p>');
  } else {
    it.setField('title', it.getField('title') + ' (touched locally)');
  }
  await it.saveTx();
  return k;
}
return null;
`

// jsCleanup erases everything a run created (items whose run tag starts with
// "zb-e2e-run-<run>", collections named "[zb-e2e] <run>…") and removes its tag
// colours; nothing else.
const jsCleanup = `
const prefix = 'zb-e2e-run-' + query.run;
const s = new Zotero.Search();
s.libraryID = L;
s.addCondition('tag', 'is', TAG);
let items = 0;
for (const it of Zotero.Items.get(await s.search())) {
  if (it.libraryID !== L || it.parentItemID) continue;
  if (!it.getTags().some(t => t.tag.startsWith(prefix))) continue;
  await it.eraseTx(); items++;
}
let colls = 0;
for (const c of Zotero.Collections.getByLibrary(L, true)) {
  if (!c.parentID && c.name.startsWith('[zb-e2e] ' + query.run)) { await c.eraseTx(); colls++; }
}
let colours = 0;
for (const t of Array.from(Zotero.Tags.getColors(L).keys())) {
  if (t.startsWith('zb-e2e-colour-' + query.run)) { await Zotero.Tags.setColor(L, t, false); colours++; }
}
return { items, colls, colours };
`

// jsPreflight checks the desktop before anything is changed.
const jsPreflight = `
const unsyncedItems = (await Zotero.Sync.Data.Local.getUnsynced('item', L)).length;
const unsyncedCollections = (await Zotero.Sync.Data.Local.getUnsynced('collection', L)).length;
return { zotero: Zotero.version, dataDir: Zotero.DataDirectory.dir, libraryID: L, editable: Zotero.Libraries.get(L).editable,
  libraryVersion: Zotero.Libraries.get(L).libraryVersion, unsyncedItems, unsyncedCollections };
`
