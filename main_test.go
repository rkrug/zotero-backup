package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeZotero answers /keys/current and /users/<id>/groups like the real API,
// with 150 groups so that paging is exercised.
func fakeZotero(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/keys/current", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Zotero-API-Key") != "secret" || r.Header.Get("Zotero-API-Version") != "3" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `{"userID":7,"username":"tester","access":{
			"user":{"library":true,"files":true,"notes":false,"write":false},
			"groups":{"all":{"library":true,"write":false},"105":{"library":true,"write":true}}}}`)
	})
	mux.HandleFunc("/users/7/groups", func(w http.ResponseWriter, r *http.Request) {
		start := 0
		fmt.Sscan(r.URL.Query().Get("start"), &start)
		w.Header().Set("Total-Results", "150")
		var parts []string
		for id := 101 + start; id <= min(150, start+100)+100; id++ {
			parts = append(parts, fmt.Sprintf(`{"id":%d,"data":{"name":"Group %d"}}`, id, id))
		}
		fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, Key: "secret", HTTP: srv.Client()}
}

func TestKeyCheck(t *testing.T) {
	var out strings.Builder
	p := defaultProfile()
	p.Library = "group:105"
	if err := keyCheck(fakeZotero(t), p, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"user tester (7)",
		"  user          read, files",           // no notes, no write; not covered by the profile
		"* group:105     read/write  Group 105", // group entry wins over "all"; covered by the profile
		"  group:250     read  Group 250",       // second page reached
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "secret") {
		t.Error("output contains the key")
	}
}

func TestKeyCheckGroupNotMember(t *testing.T) {
	p := defaultProfile()
	p.Library = "group:999"
	err := keyCheck(fakeZotero(t), p, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Errorf("err = %v", err)
	}
}

func TestKeyCheckWrongKey(t *testing.T) {
	c := fakeZotero(t)
	c.Key = "wrong"
	err := keyCheck(c, defaultProfile(), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("err = %v", err)
	}
}
