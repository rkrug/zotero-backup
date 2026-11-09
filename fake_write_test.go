package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func objVersion(d map[string]any) int {
	if v, ok := d["version"].(float64); ok {
		return int(v)
	}
	return 10
}

// serveWrite handles the write requests restore makes, like the Zotero API:
// per-object version preconditions, write tokens, settings, full text and the
// three-step file upload (authorise, upload to storage, register).
func (f *fakeLibrary) serveWrite(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	body, _ := io.ReadAll(r.Body)
	if strings.HasPrefix(p, "/storage/") { // the storage service: no API key expected
		if r.Header.Get("Zotero-API-Key") != "" {
			http.Error(w, "API key sent to storage", http.StatusBadRequest)
			return
		}
		up := strings.TrimPrefix(p, "/storage/")
		f.pending[up] = strings.TrimSuffix(strings.TrimPrefix(string(body), "PREFIX"), "SUFFIX")
		f.uploads++
		w.WriteHeader(http.StatusCreated)
		return
	}
	if f.readOnly {
		http.Error(w, "Write access denied", http.StatusForbidden)
		return
	}
	switch {
	case r.Method == "POST" && (p == "/groups/1/items" || p == "/groups/1/collections"):
		if tok := r.Header.Get("Zotero-Write-Token"); f.tokens[tok] != nil {
			w.Write(f.tokens[tok])
			return
		}
		objs := f.items
		if strings.HasSuffix(p, "/collections") {
			objs = f.collections
		}
		var list []map[string]any
		json.Unmarshal(body, &list)
		res := map[string]any{"success": map[string]string{}, "unchanged": map[string]string{}, "failed": map[string]any{}}
		for i, o := range list {
			key, _ := o["key"].(string)
			ver := int(o["version"].(float64))
			cur, exists := objs[key]
			if (ver == 0 && exists) || (exists && ver != objVersion(cur)) || (!exists && ver != 0) {
				res["failed"].(map[string]any)[strconv.Itoa(i)] = map[string]any{"key": key, "code": 412, "message": "version mismatch"}
				continue
			}
			next := map[string]any{}
			if exists {
				next = maps.Clone(cur)
			}
			for k, v := range o {
				next[k] = v
			}
			if next["deleted"] == false {
				delete(next, "deleted")
			}
			f.version++
			next["version"] = float64(f.version)
			objs[key] = next
			res["success"].(map[string]string)[strconv.Itoa(i)] = key
		}
		b, _ := json.Marshal(res)
		f.tokens[r.Header.Get("Zotero-Write-Token")] = b
		w.Write(b)
	case r.Method == "POST" && p == "/groups/1/settings":
		if r.Header.Get("If-Unmodified-Since-Version") != strconv.Itoa(f.version) {
			http.Error(w, "Library has been modified", http.StatusPreconditionFailed)
			return
		}
		cur := map[string]any{}
		json.Unmarshal([]byte(f.settings), &cur)
		var set map[string]any
		json.Unmarshal(body, &set)
		f.version++
		for k, v := range set {
			v.(map[string]any)["version"] = f.version
			cur[k] = v
		}
		b, _ := json.Marshal(cur)
		f.settings = string(b)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "PUT" && strings.HasSuffix(p, "/fulltext"):
		f.fulltext[strings.Split(p, "/")[4]] = string(body)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && strings.HasSuffix(p, "/file"):
		key := strings.Split(p, "/")[4]
		item := f.items[key]
		form, _ := url.ParseQuery(string(body))
		cur, _ := item["md5"].(string)
		if (r.Header.Get("If-None-Match") == "*" && cur != "") || (r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != cur) {
			http.Error(w, "precondition", http.StatusPreconditionFailed)
			return
		}
		if up := form.Get("upload"); up != "" { // register
			content, auth := f.pending[up], f.authorised[key]
			sum := auth.Get("md5")
			if z := auth.Get("zipMD5"); z != "" {
				sum = z
			}
			if md5hex(content) != sum {
				http.Error(w, "md5 of the uploaded content does not match", http.StatusBadRequest)
				return
			}
			f.files[key] = content
			item["md5"] = auth.Get("md5")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		f.authorised[key] = form
		md5 := form.Get("md5")
		for k, c := range f.files { // the storage service already has this content
			if md5hex(c) == md5 || f.items[k] != nil && f.items[k]["md5"] == md5 {
				f.files[key] = c
				item["md5"] = md5
				fmt.Fprint(w, `{"exists":1}`)
				return
			}
		}
		u := "http://" + r.Host + "/storage/" + key
		json.NewEncoder(w).Encode(map[string]string{"url": u, "contentType": "application/octet-stream",
			"prefix": "PREFIX", "suffix": "SUFFIX", "uploadKey": key})
	default:
		http.Error(w, "unexpected write "+r.Method+" "+p, http.StatusTeapot)
	}
}
