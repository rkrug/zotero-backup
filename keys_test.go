package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// twoKeyServer: user 7 is a member of groups 1 and 2. Key K1 may read group 1
// only; key K2 may read and write group 2.
func twoKeyServer(t *testing.T) func(string) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys/current":
			switch r.Header.Get("Zotero-API-Key") {
			case "K1":
				fmt.Fprint(w, `{"userID":7,"username":"tester","access":{"user":{"library":false},"groups":{"1":{"library":true}}}}`)
			case "K2":
				fmt.Fprint(w, `{"userID":7,"username":"tester","access":{"user":{"library":false},"groups":{"2":{"library":true,"write":true}}}}`)
			default:
				http.Error(w, "Forbidden", http.StatusForbidden)
			}
		case "/users/7/groups":
			w.Header().Set("Total-Results", "2")
			fmt.Fprint(w, `[{"id":1,"data":{"name":"One"}},{"id":2,"data":{"name":"Two"}}]`)
		default:
			http.Error(w, "unexpected", http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return func(key string) *Client { return &Client{Base: srv.URL, Key: key, HTTP: srv.Client()} }
}

func TestLibraryJobsUseEachLibrarysKey(t *testing.T) {
	store := fakeKeychain(t)
	store["readonly"], store["writer"] = "K1", "K2"
	t.Setenv("ZOTERO_API_KEY", "")
	p := defaultProfile()
	p.Key, p.Keys = "readonly", map[string]string{"group:2": "writer"}

	jobs, skipped, err := libraryJobs(p, clientsFor(p, twoKeyServer(t)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range jobs {
		got[j.lib.ID] = j.c.Key
	}
	if len(jobs) != 2 || got["group-1"] != "K1" || got["group-2"] != "K2" {
		t.Errorf("jobs %v", got)
	}
	if strings.Contains(strings.Join(skipped, " "), "group-2") || !strings.Contains(strings.Join(skipped, " "), "user-7") {
		t.Errorf("skipped %v", skipped)
	}

	// a single library uses its own key too
	p.Library = "group:2"
	jobs, _, err = libraryJobs(p, clientsFor(p, twoKeyServer(t)))
	if err != nil || len(jobs) != 1 || jobs[0].c.Key != "K2" {
		t.Errorf("group:2: %v %v", jobs, err)
	}

	// a key that cannot read its library is reported, not fatal
	p.Library, p.Keys = "all", map[string]string{"group:2": "readonly"}
	_, skipped, err = libraryJobs(p, clientsFor(p, twoKeyServer(t)))
	if err != nil || !strings.Contains(strings.Join(skipped, " "), `group:2 (key "readonly")`) {
		t.Errorf("skipped %v, err %v", skipped, err)
	}
}

func TestRestoreAndKeyCheckUseTheLibrarysKey(t *testing.T) {
	store := fakeKeychain(t)
	store["readonly"], store["writer"] = "K1", "K2"
	t.Setenv("ZOTERO_API_KEY", "")
	p := defaultProfile()
	p.Key, p.Keys = "readonly", map[string]string{"group:2": "writer"}
	newClientFor := twoKeyServer(t)

	b := &backupSet{meta: Meta{Library: "group-2"}}
	c, err := clientsFor(p, newClientFor)(b.spec())
	if err != nil || c.Key != "K2" || canWrite(c, "/groups/2") != nil {
		t.Errorf("client for the backup's library: key %q, err %v", c.Key, err)
	}

	var out strings.Builder
	if err := keyCheckAll(p, newClientFor, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`== key "readonly", for: default`, `== key "writer", for: group:2`, "read/write  Two"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("key check lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRestoreUsesWriteKey(t *testing.T) {
	store := fakeKeychain(t)
	store["readonly"], store["writer"] = "K1", "K2"
	t.Setenv("ZOTERO_API_KEY", "")
	p := defaultProfile()
	p.Key, p.WriteKey = "readonly", "writer"
	newClientFor := twoKeyServer(t)

	if c, _ := clientsFor(p, newClientFor)("group:2"); c.Key != "K1" {
		t.Errorf("backup / diff use %q, want the read-only key", c.Key)
	}
	if c, _ := clientsFor(p.forRestore(), newClientFor)("group:2"); c.Key != "K2" {
		t.Errorf("restore uses %q, want the write key", c.Key)
	}
	var out strings.Builder
	keyCheckAll(p, newClientFor, &out)
	if !strings.Contains(out.String(), `== key "writer", for: restore`) {
		t.Errorf("key check:\n%s", out.String())
	}
	if q := defaultProfile(); q.forRestore().Key != q.Key {
		t.Error("without write_key, restore must keep the profile's keys")
	}
}
