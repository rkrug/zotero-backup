package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.zotero.org"

// Client talks to the Zotero web API (version 3). It is safe for concurrent use.
type Client struct {
	Base     string // apiBase, or a test server
	Key      string
	HTTP     *http.Client
	Attempts int                 // tries per request before giving up (0: 5)
	Sleep    func(time.Duration) // nil: time.Sleep; replaced in tests

	mu      sync.Mutex
	backoff time.Duration // asked for by the server; waited before the next request
}

func newClient(key string) *Client {
	return &Client{Base: apiBase, Key: key, HTTP: &http.Client{Timeout: 10 * time.Minute}, Attempts: 5, Sleep: time.Sleep}
}

// HTTPError is a final (non-retried, or retried in vain) HTTP error.
type HTTPError struct {
	Path string
	Code int
	Body string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("GET %s: HTTP %d: %s", e.Path, e.Code, e.Body) }

// errLibraryChanged means the library was modified while its pages were read.
var errLibraryChanged = errors.New("library changed while reading; read it again")

// get performs a GET and returns the body and headers of a 200 response.
// It retries on network errors, 429 and 5xx (waiting Retry-After, else 1, 2, 4 … s)
// and honours Backoff by waiting that long before the next request.
// ponytail: whole body in memory; stream when downloading large files.
func (c *Client) get(path string) ([]byte, http.Header, error) {
	attempts := c.Attempts
	if attempts < 1 {
		attempts = 5
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		c.waitBackoff()
		body, h, code, err := c.once(path)
		if h != nil {
			c.setBackoff(h.Get("Backoff"))
		}
		switch {
		case err != nil: // network: connection reset, timeout, DNS
			lastErr = fmt.Errorf("GET %s: %w", path, err)
		case code == http.StatusOK:
			return body, h, nil
		case code == http.StatusTooManyRequests || code >= 500:
			lastErr = &HTTPError{path, code, strings.TrimSpace(string(body))}
			if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
				c.sleep(time.Duration(s) * time.Second)
				continue
			}
		default: // 4xx: retrying will not help
			return nil, h, &HTTPError{path, code, strings.TrimSpace(string(body))}
		}
		if attempt < attempts-1 {
			c.sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	return nil, nil, fmt.Errorf("giving up after %d attempts: %w", attempts, lastErr)
}

func (c *Client) once(path string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequest("GET", c.Base+path, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Zotero-API-Version", "3")
	req.Header.Set("Zotero-API-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil { // connection dropped mid-body
		return nil, resp.Header, 0, err
	}
	return body, resp.Header, resp.StatusCode, nil
}

func (c *Client) setBackoff(v string) {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		c.mu.Lock()
		c.backoff = max(c.backoff, time.Duration(s)*time.Second)
		c.mu.Unlock()
	}
}

func (c *Client) waitBackoff() {
	c.mu.Lock()
	d := c.backoff
	c.backoff = 0
	c.mu.Unlock()
	if d > 0 {
		c.sleep(d)
	}
}

func (c *Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// getJSON decodes a GET response into v and returns the response headers.
func (c *Client) getJSON(path string, v any) (http.Header, error) {
	body, h, err := c.get(path)
	if err != nil {
		return h, err
	}
	return h, json.Unmarshal(body, v)
}

// getAll reads every page of a multi-object request (100 per page) and returns
// the objects and the library version. It fails if the count does not match
// Total-Results, and with errLibraryChanged if the version moved between pages.
func getAll[T any](c *Client, path string) ([]T, int, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	version := -1
	for {
		var page []T
		h, err := c.getJSON(fmt.Sprintf("%s%slimit=100&start=%d", path, sep, len(all)), &page)
		if err != nil {
			return nil, 0, err
		}
		v, _ := strconv.Atoi(h.Get("Last-Modified-Version"))
		if version == -1 {
			version = v
		} else if v != version {
			return nil, 0, errLibraryChanged
		}
		total, err := strconv.Atoi(h.Get("Total-Results"))
		if err != nil {
			return nil, 0, fmt.Errorf("GET %s: no Total-Results header", path)
		}
		all = append(all, page...)
		if len(page) == 0 || len(all) >= total {
			if len(all) != total {
				return nil, 0, fmt.Errorf("GET %s: got %d objects, Total-Results says %d", path, len(all), total)
			}
			return all, version, nil
		}
	}
}

// Access is what a key may do in one library.
type Access struct {
	Library bool `json:"library"`
	Files   bool `json:"files"`
	Notes   bool `json:"notes"`
	Write   bool `json:"write"`
}

// KeyInfo is the answer of GET /keys/current.
type KeyInfo struct {
	UserID   int    `json:"userID"`
	Username string `json:"username"`
	Access   struct {
		User   Access            `json:"user"`
		Groups map[string]Access `json:"groups"` // "all" or a group ID
	} `json:"access"`
}

// GroupAccess returns the key's rights for one group: a group-specific entry
// wins over "all".
func (k KeyInfo) GroupAccess(id int) Access {
	if a, ok := k.Access.Groups[strconv.Itoa(id)]; ok {
		return a
	}
	return k.Access.Groups["all"]
}

func (c *Client) keyInfo() (KeyInfo, error) {
	var k KeyInfo
	_, err := c.getJSON("/keys/current", &k)
	return k, err
}

// Group is one group the key's user is a member of.
type Group struct {
	ID   int `json:"id"`
	Data struct {
		Name string `json:"name"`
	} `json:"data"`
}

// groups lists all groups of the user.
func (c *Client) groups(userID int) ([]Group, error) {
	g, _, err := getAll[Group](c, fmt.Sprintf("/users/%d/groups", userID))
	return g, err
}
