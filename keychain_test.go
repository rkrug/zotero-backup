package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakeKeychain replaces the system keychain for the test.
func fakeKeychain(t *testing.T) map[string]string {
	store := map[string]string{}
	get, set, del := keychainGet, keychainSet, keychainDelete
	keychainGet = func(n string) (string, error) {
		if k, ok := store[n]; ok {
			return k, nil
		}
		return "", errNoEntry
	}
	keychainSet = func(n, k string) error { store[n] = k; return nil }
	keychainDelete = func(n string) error {
		if _, ok := store[n]; !ok {
			return errNoEntry
		}
		delete(store, n)
		return nil
	}
	t.Cleanup(func() { keychainGet, keychainSet, keychainDelete = get, set, del })
	return store
}

// stdinWith returns a pipe that reads s, like a key piped into the command.
func stdinWith(t *testing.T, s string) *os.File {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString(s)
	w.Close()
	t.Cleanup(func() { r.Close() })
	return r
}

func TestAPIKeyFromEnvironmentFirst(t *testing.T) {
	store := fakeKeychain(t)
	store["zotero-backup"] = "from-keychain"
	p := defaultProfile()
	t.Setenv("ZOTERO_API_KEY", "from-env")
	if k, _ := apiKey(p); k != "from-env" {
		t.Errorf("key %q", k)
	}
	t.Setenv("ZOTERO_API_KEY", "")
	if k, _ := apiKey(p); k != "from-keychain" {
		t.Errorf("key %q", k)
	}
	p.Key = "other"
	if _, err := apiKey(p); err == nil || !strings.Contains(err.Error(), "key set other") {
		t.Errorf("err = %v", err)
	}
}

func TestKeySetChecksAndStores(t *testing.T) {
	store := fakeKeychain(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Zotero-API-Key") != "good-key" {
			http.Error(w, "Invalid key", http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"userID":7,"username":"tester","access":{}}`))
	}))
	defer srv.Close()
	client := func(k string) *Client { return &Client{Base: srv.URL, Key: k, HTTP: srv.Client()} }

	var out strings.Builder
	if err := keySet("readonly", client, stdinWith(t, "good-key\n"), &out); err != nil {
		t.Fatal(err)
	}
	if store["readonly"] != "good-key" || strings.Contains(out.String(), "good-key") || !strings.Contains(out.String(), "tester (7)") {
		t.Errorf("store %v, output %q", store, out.String())
	}
	if err := keySet("bad", client, stdinWith(t, "wrong\n"), &out); err == nil || store["bad"] != "" {
		t.Errorf("a rejected key was stored: %v %v", err, store)
	}
	if err := keySet("empty", client, stdinWith(t, ""), &out); err == nil {
		t.Error("empty input accepted")
	}
	if err := keyRemove("readonly", &out); err != nil || store["readonly"] != "" {
		t.Errorf("remove: %v %v", err, store)
	}
	if err := keyRemove("readonly", &out); !errors.Is(err, errNoEntry) {
		t.Errorf("removing twice: %v", err)
	}
}

// TestOSKeychain uses the real system keychain; it runs only with
// ZB_KEYCHAIN_TEST=1 (CI sets it), with a random entry name it removes again.
func TestOSKeychain(t *testing.T) {
	if os.Getenv("ZB_KEYCHAIN_TEST") != "1" {
		t.Skip("set ZB_KEYCHAIN_TEST=1 to test the system keychain")
	}
	b := make([]byte, 6)
	rand.Read(b)
	name := "zotero-backup-test-" + hex.EncodeToString(b)
	if _, err := osKeychainGet(name); !errors.Is(err, errNoEntry) {
		t.Fatalf("get before set: %v", err)
	}
	if err := osKeychainSet(name, `key with "quotes" and spaces`); err != nil {
		t.Fatal(err)
	}
	defer osKeychainDelete(name)
	if k, err := osKeychainGet(name); err != nil || k != `key with "quotes" and spaces` {
		t.Errorf("get: %q %v", k, err)
	}
	if err := osKeychainSet(name, "replaced"); err != nil {
		t.Fatal(err)
	}
	if k, _ := osKeychainGet(name); k != "replaced" {
		t.Errorf("after replacing: %q", k)
	}
	if err := osKeychainDelete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := osKeychainGet(name); !errors.Is(err, errNoEntry) {
		t.Errorf("get after delete: %v", err)
	}
}
