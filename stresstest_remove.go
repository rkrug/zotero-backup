package main

import (
	"fmt"
	"strings"
)

// removeNewChecks (part of the extras): objects made after the backup (a new
// item with a child note, a note under a fixture item, a collection with a
// subcollection) are moved to the trash by restore --trash-new, then erased by
// restore --erase-new, on the server and, after a sync, in Zotero desktop.
// Before each restore the plan is checked: it may remove only this test's own
// objects, else the step is refused.
func (s *stress) removeNewChecks() {
	const sc, op = "extras", "remove-new"
	fams, err := s.families("remove", []stressOp{stressOps[0]})
	if err != nil {
		s.record(sc, op, "fixture", false, "%v", err)
		return
	}
	f := fams[0]
	s.sync(sc, "sync the fixture", "fail")
	b0, err := s.backup()
	if err != nil {
		s.record(sc, op, "backup", false, "%v", err)
		return
	}

	item, err := s.api.newItem("[zb-e2e] new after backup (remove-new)", f.runTag())
	var keys []string
	if err == nil {
		var res WriteResult
		res, err = s.c.writeObjects(s.lib.Path, "items", []map[string]any{
			{"itemType": "note", "parentItem": item, "note": "<p>[zb-e2e] child of a new item</p>"},
			{"itemType": "note", "parentItem": f.Parent, "note": "<p>[zb-e2e] new note under a fixture item</p>"}})
		keys = append([]string{item}, res.Written...)
		if err == nil && len(res.Written) != 2 {
			err = fmt.Errorf("notes not written: %v", res.Failed)
		}
	}
	var colls []string
	if err == nil {
		var res WriteResult
		res, err = s.c.writeObjects(s.lib.Path, "collections", []map[string]any{{"name": "[zb-e2e] " + s.run + " remove-new"}})
		if err == nil && len(res.Written) == 1 {
			colls = res.Written
			res, err = s.c.writeObjects(s.lib.Path, "collections", []map[string]any{{"name": "[zb-e2e] " + s.run + " remove-new sub", "parentCollection": colls[0]}})
			colls = append(colls, res.Written...)
		}
		if err == nil && len(colls) != 2 {
			err = fmt.Errorf("collections not written: %v", res.Failed)
		}
	}
	if err != nil {
		s.record(sc, op, "new objects on the server", false, "%v", err)
		return
	}
	s.sync(sc, "sync the new objects", "fail")
	all := append(append([]string{}, keys...), colls...)

	for _, mode := range []string{"trash", "erase"} {
		flag := "--" + mode + "-new"
		now, err := readLibrary(s.c, s.lib.Path)
		if err != nil {
			s.record(sc, op, flag+": plan", false, "%v", err)
			return
		}
		p := withRemovals(makePlan(b0, now, nil, nil), now, mode)
		if foreign := notOwn(p.remove, now); len(foreign) > 0 {
			s.record(sc, op, flag+": would remove only this test's objects", false, "refused, not from this test: %v", foreign)
			return
		}
		var got []string
		for _, ch := range p.remove {
			got = append(got, ch.Key)
		}
		s.record(sc, op, flag+": plan lists exactly the new objects", sameSet(got, all), "plan %v, new %v", got, all)
		var out strings.Builder
		err = runRestore(fixedClient(s.c), b0.dir, s.profile, nil, mode, false, true, &out, quietProgress())
		s.record(sc, op, flag+": restore matches the backup", err == nil && strings.Contains(out.String(), "checked: the library matches the backup"), "%v\n%s", err, out.String())
		after, err := readLibrary(s.c, s.lib.Path)
		if err != nil {
			s.record(sc, op, flag+": read the library", false, "%v", err)
			return
		}
		var wrong []string
		for _, k := range all {
			d := after.items[k]
			field := "parentItem"
			if d == nil {
				d, field = after.collections[k], "parentCollection"
			}
			objs := after.items
			if field == "parentCollection" {
				objs = after.collections
			}
			switch {
			case mode == "trash" && (d == nil || !trashedWithParents(objs, k, field)):
				wrong = append(wrong, k+" not in the trash")
			case mode == "erase" && d != nil:
				wrong = append(wrong, k+" still there")
			}
		}
		s.record(sc, op, flag+": server", len(wrong) == 0, "%v", wrong)
		s.sync(sc, "sync after "+flag, "fail")
		var desk map[string]string
		err = s.z.run(jsTrashState, map[string]string{"keys": strings.Join(all, ",")}, &desk)
		wrong = nil
		for _, k := range all {
			if w := map[string]string{"trash": "trash", "erase": "gone"}[mode]; desk[k] != w {
				wrong = append(wrong, k+": "+desk[k])
			}
		}
		s.record(sc, op, flag+": Zotero desktop", err == nil && len(wrong) == 0, "%v %v", err, wrong)
	}
}

// notOwn lists objects that are not this test's: items whose top-level item
// lacks the tag zb-e2e, collections not named "[zb-e2e]…", any search.
func notOwn(cs []change, now *libraryNow) []string {
	var out []string
	for _, c := range cs {
		switch c.Kind {
		case "items":
			top := c.Key
			for n := 0; n < 100; n++ {
				p, _ := now.items[top]["parentItem"].(string)
				if p == "" {
					break
				}
				top = p
			}
			if !hasTag(now.items[top], "zb-e2e") {
				out = append(out, c.Key)
			}
		case "collections":
			if name, _ := now.collections[c.Key]["name"].(string); !strings.HasPrefix(name, "[zb-e2e]") {
				out = append(out, c.Key)
			}
		default:
			out = append(out, c.Kind+" "+c.Key)
		}
	}
	return out
}

func hasTag(d map[string]any, tag string) bool {
	tags, _ := d["tags"].([]any)
	for _, t := range tags {
		if m, _ := t.(map[string]any); m["tag"] == tag {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		if seen[x] == 0 {
			return false
		}
		seen[x]--
	}
	return true
}

// jsTrashState tells for each key (items or collections) whether Zotero
// desktop has it ("present"), has it in the trash, also through a trashed
// parent ("trash"), or not at all ("gone").
const jsTrashState = `
const out = {};
for (const k of query.keys.split(',')) {
  const it = await Zotero.Items.getByLibraryAndKeyAsync(L, k);
  if (it) {
    let p = it, t = false;
    while (p) { if (p.deleted) { t = true; break; } p = p.parentItem; }
    out[k] = t ? 'trash' : 'present';
    continue;
  }
  const c = await Zotero.Collections.getByLibraryAndKeyAsync(L, k);
  out[k] = !c ? 'gone' : (c.deleted ? 'trash' : 'present');
}
return out;
`
