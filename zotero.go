package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const apiBase = "https://api.zotero.org"

// Client talks to the Zotero web API (version 3).
type Client struct {
	Base string // apiBase, or a test server
	Key  string
	HTTP *http.Client
}

func newClient(key string) *Client {
	return &Client{Base: apiBase, Key: key, HTTP: http.DefaultClient}
}

// getJSON decodes a GET response into v and returns the response headers.
// ponytail: no retries yet; Backoff / Retry-After / network retries come with the backup step.
func (c *Client) getJSON(path string, v any) (http.Header, error) {
	req, err := http.NewRequest("GET", c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Zotero-API-Version", "3")
	req.Header.Set("Zotero-API-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return resp.Header, fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, body)
	}
	return resp.Header, json.NewDecoder(resp.Body).Decode(v)
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

// groups lists all groups of the user, following the pages.
func (c *Client) groups(userID int) ([]Group, error) {
	var all []Group
	for {
		var page []Group
		h, err := c.getJSON(fmt.Sprintf("/users/%d/groups?limit=100&start=%d", userID, len(all)), &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		total, _ := strconv.Atoi(h.Get("Total-Results"))
		if len(page) == 0 || len(all) >= total {
			return all, nil
		}
	}
}
