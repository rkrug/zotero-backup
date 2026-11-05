package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClient returns a client for h that records its sleeps instead of sleeping.
func testClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var mu sync.Mutex
	var slept []time.Duration
	c := &Client{Base: srv.URL, Key: "k", HTTP: srv.Client(), Attempts: 5,
		Sleep: func(d time.Duration) { mu.Lock(); slept = append(slept, d); mu.Unlock() }}
	return c, &slept
}

// dropConnection closes the connection without an HTTP response.
func dropConnection(w http.ResponseWriter) {
	conn, _, _ := w.(http.Hijacker).Hijack()
	conn.Close()
}

func secs(s ...int) []time.Duration {
	var d []time.Duration
	for _, x := range s {
		d = append(d, time.Duration(x)*time.Second)
	}
	return d
}

func TestRetryAfterOn503(t *testing.T) {
	calls := 0
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls++; calls == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "ok")
	})
	body, _, err := c.get("/x")
	if err != nil || string(body) != "ok" || calls != 2 || !slices.Equal(*slept, secs(7)) {
		t.Errorf("body %q err %v calls %d slept %v", body, err, calls, *slept)
	}
}

func TestRetryOnDroppedConnection(t *testing.T) {
	calls := 0
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls++; calls == 1 {
			dropConnection(w)
			return
		}
		fmt.Fprint(w, "ok")
	})
	body, _, err := c.get("/x")
	if err != nil || string(body) != "ok" || calls != 2 || !slices.Equal(*slept, secs(1)) {
		t.Errorf("body %q err %v calls %d slept %v", body, err, calls, *slept)
	}
}

func TestRetryOnConnectionDroppedMidBody(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls++; calls == 1 {
			conn, buf, _ := w.(http.Hijacker).Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
			buf.Flush()
			conn.Close()
			return
		}
		fmt.Fprint(w, "complete")
	})
	body, _, err := c.get("/x")
	if err != nil || string(body) != "complete" || calls != 2 {
		t.Errorf("body %q err %v calls %d", body, err, calls)
	}
}

func TestBackoffDelaysNextRequest(t *testing.T) {
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			w.Header().Set("Backoff", "30")
		}
		fmt.Fprint(w, "ok")
	})
	if _, _, err := c.get("/first"); err != nil || len(*slept) != 0 {
		t.Fatalf("first request: err %v slept %v", err, *slept)
	}
	if _, _, err := c.get("/second"); err != nil || !slices.Equal(*slept, secs(30)) {
		t.Errorf("second request: err %v slept %v, want 30s before it", err, *slept)
	}
}

func TestGivesUpAfterAttempts(t *testing.T) {
	calls := 0
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "busy", http.StatusServiceUnavailable)
	})
	_, _, err := c.get("/x")
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != 503 || !strings.Contains(err.Error(), "giving up after 5 attempts") {
		t.Errorf("err = %v", err)
	}
	if calls != 5 || !slices.Equal(*slept, secs(1, 2, 4, 8)) {
		t.Errorf("calls %d slept %v", calls, *slept)
	}
}

func TestNoRetryOn404(t *testing.T) {
	calls := 0
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "Not found", http.StatusNotFound)
	})
	_, _, err := c.get("/x")
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != 404 || calls != 1 || len(*slept) != 0 {
		t.Errorf("err %v calls %d slept %v", err, calls, *slept)
	}
}

// pagedServer serves n objects {"key":"K<i>"} in pages; version(page) gives
// Last-Modified-Version, total overrides Total-Results when > 0.
func pagedServer(n, total int, version func(start int) int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if total == 0 {
			total = n
		}
		w.Header().Set("Total-Results", strconv.Itoa(total))
		w.Header().Set("Last-Modified-Version", strconv.Itoa(version(start)))
		var parts []string
		for i := start; i < min(n, start+limit); i++ {
			parts = append(parts, fmt.Sprintf(`{"key":"K%d"}`, i))
		}
		fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
	}
}

type keyed struct {
	Key string `json:"key"`
}

func TestGetAllReadsEveryPage(t *testing.T) {
	calls := 0
	h := pagedServer(250, 0, func(int) int { return 42 })
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("includeTrashed") != "1" {
			t.Errorf("query lost: %s", r.URL.RawQuery)
		}
		h(w, r)
	})
	objs, v, err := getAll[keyed](c, "/groups/1/items?includeTrashed=1")
	if err != nil || len(objs) != 250 || v != 42 || calls != 3 || objs[249].Key != "K249" {
		t.Errorf("len %d version %d calls %d err %v", len(objs), v, calls, err)
	}
}

func TestGetAllDetectsChangeBetweenPages(t *testing.T) {
	c, _ := testClient(t, pagedServer(250, 0, func(start int) int { return 42 + start/100 }))
	if _, _, err := getAll[keyed](c, "/x"); !errors.Is(err, errLibraryChanged) {
		t.Errorf("err = %v, want errLibraryChanged", err)
	}
}

func TestGetAllDetectsMissingObjects(t *testing.T) {
	c, _ := testClient(t, pagedServer(120, 150, func(int) int { return 1 }))
	_, _, err := getAll[keyed](c, "/x")
	if err == nil || !strings.Contains(err.Error(), "got 120 objects, Total-Results says 150") {
		t.Errorf("err = %v", err)
	}
}
