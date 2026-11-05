package main

// Writes to the Zotero API: objects, settings, full text and files.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// write sends one write request and returns status, headers and body. It
// retries 429 and 5xx (waiting Retry-After or 1, 2, 4 … s) and, only when
// retrying is safe (retryNet), network errors: multi-object POSTs carry a
// Zotero-Write-Token, so the server answers a repeated request from its cache
// instead of applying it twice.
func (c *Client) write(method, path string, body []byte, header map[string]string, retryNet bool) (int, http.Header, []byte, error) {
	attempts := c.Attempts
	if attempts < 1 {
		attempts = 5
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		c.waitBackoff()
		code, h, b, err := c.writeOnce(method, path, body, header)
		if h != nil {
			c.setBackoff(h.Get("Backoff"))
		}
		switch {
		case err != nil && !retryNet:
			return 0, nil, nil, fmt.Errorf("%s %s: %w (not retried: the server may have applied it)", method, path, err)
		case err != nil:
			lastErr = fmt.Errorf("%s %s: %w", method, path, err)
		case code == http.StatusTooManyRequests || code >= 500:
			lastErr = &HTTPError{path, code, strings.TrimSpace(string(b))}
			if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
				c.sleep(time.Duration(s) * time.Second)
				continue
			}
		default:
			return code, h, b, nil
		}
		if attempt < attempts-1 {
			c.sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	return 0, nil, nil, fmt.Errorf("giving up after %d attempts: %w", attempts, lastErr)
}

func (c *Client) writeOnce(method, path string, body []byte, header map[string]string) (int, http.Header, []byte, error) {
	req, err := http.NewRequest(method, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Zotero-API-Version", "3")
	req.Header.Set("User-Agent", "zotero-backup/"+version)
	req.Header.Set("Zotero-API-Key", c.Key)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b, err
}

func writeToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteResult is the outcome of writing objects: keys written, keys the server
// found unchanged, and failures by key.
type WriteResult struct {
	Written   []string
	Unchanged []string
	Failed    map[string]string
}

// writeObjects POSTs objects (items, collections or searches) in batches of 50.
// Each object carries its own "version" as the precondition.
func (c *Client) writeObjects(libPath, kind string, objs []map[string]any) (WriteResult, error) {
	res := WriteResult{Failed: map[string]string{}}
	for i := 0; i < len(objs); i += 50 {
		batch := objs[i:min(i+50, len(objs))]
		body, err := json.Marshal(batch)
		if err != nil {
			return res, err
		}
		code, _, b, err := c.write("POST", libPath+"/"+kind, body,
			map[string]string{"Content-Type": "application/json", "Zotero-Write-Token": writeToken()}, true)
		if err != nil {
			return res, err
		}
		if code != http.StatusOK {
			return res, &HTTPError{libPath + "/" + kind, code, strings.TrimSpace(string(b))}
		}
		var r struct {
			Success   map[string]string `json:"success"`
			Unchanged map[string]string `json:"unchanged"`
			Failed    map[string]struct {
				Key     string `json:"key"`
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"failed"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return res, fmt.Errorf("POST %s/%s: %w", libPath, kind, err)
		}
		for _, k := range r.Success {
			res.Written = append(res.Written, k)
		}
		for _, k := range r.Unchanged {
			res.Unchanged = append(res.Unchanged, k)
		}
		for idx, f := range r.Failed {
			key := f.Key
			if n, err := strconv.Atoi(idx); key == "" && err == nil && n < len(batch) {
				key, _ = batch[n]["key"].(string)
			}
			res.Failed[key] = fmt.Sprintf("HTTP %d: %s", f.Code, f.Message)
		}
	}
	return res, nil
}

// writeSettings POSTs settings ({"name": {"value": …}}) with the library
// version as precondition.
func (c *Client) writeSettings(libPath string, settings map[string]any, libVersion int) error {
	body, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	code, _, b, err := c.write("POST", libPath+"/settings", body,
		map[string]string{"Content-Type": "application/json", "If-Unmodified-Since-Version": strconv.Itoa(libVersion)}, false)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent {
		return &HTTPError{libPath + "/settings", code, strings.TrimSpace(string(b))}
	}
	return nil
}

// writeFulltext PUTs an attachment's full-text content (safe to repeat).
func (c *Client) writeFulltext(libPath, key string, content []byte) error {
	code, _, b, err := c.write("PUT", libPath+"/items/"+key+"/fulltext", content,
		map[string]string{"Content-Type": "application/json"}, true)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent {
		return &HTTPError{libPath + "/items/" + key + "/fulltext", code, strings.TrimSpace(string(b))}
	}
	return nil
}

// errFileChanged means the attachment's file on the server is not the one the
// upload expected (someone changed it meanwhile).
var errFileChanged = errors.New("the file on the server changed meanwhile")

// uploadFile attaches the file at path to attachment key: authorise, upload to
// the storage service, register. serverMD5 is the md5 the server has now ("" for
// none). It returns true if the server already had the content (no upload).
func (c *Client) uploadFile(libPath, key, path, filename, md5 string, mtime int64, serverMD5 string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	cond := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "If-None-Match": "*"}
	if serverMD5 != "" {
		cond = map[string]string{"Content-Type": "application/x-www-form-urlencoded", "If-Match": serverMD5}
	}
	form := url.Values{"md5": {md5}, "filename": {filename}, "filesize": {strconv.FormatInt(st.Size(), 10)}, "mtime": {strconv.FormatInt(mtime, 10)}}
	p := libPath + "/items/" + key + "/file"
	code, _, b, err := c.write("POST", p, []byte(form.Encode()), cond, true) // authorising twice is harmless
	if err != nil {
		return false, err
	}
	if code == http.StatusPreconditionFailed {
		return false, errFileChanged
	}
	if code != http.StatusOK {
		return false, &HTTPError{p, code, strings.TrimSpace(string(b))}
	}
	var auth struct {
		Exists      int    `json:"exists"`
		URL         string `json:"url"`
		ContentType string `json:"contentType"`
		Prefix      string `json:"prefix"`
		Suffix      string `json:"suffix"`
		UploadKey   string `json:"uploadKey"`
	}
	if err := json.Unmarshal(b, &auth); err != nil {
		return false, fmt.Errorf("POST %s: %w", p, err)
	}
	if auth.Exists == 1 {
		return true, nil
	}
	if err := c.toStorage(auth.URL, auth.ContentType, auth.Prefix, path, auth.Suffix); err != nil {
		return false, err
	}
	code, _, b, err = c.write("POST", p, []byte(url.Values{"upload": {auth.UploadKey}}.Encode()), cond, true)
	if err != nil {
		return false, err
	}
	if code != http.StatusNoContent {
		return false, &HTTPError{p + " (register)", code, strings.TrimSpace(string(b))}
	}
	return false, nil
}

// toStorage streams prefix + file + suffix to the storage service. It sends no
// API key: the URL is the storage service's, not Zotero's API.
func (c *Client) toStorage(u, contentType, prefix, path, suffix string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		st, _ := f.Stat()
		req, err := http.NewRequest("POST", u, io.MultiReader(strings.NewReader(prefix), f, strings.NewReader(suffix)))
		if err != nil {
			f.Close()
			return err
		}
		req.ContentLength = int64(len(prefix)) + st.Size() + int64(len(suffix))
		req.Header.Set("Content-Type", contentType)
		resp, err := c.HTTP.Do(req)
		f.Close()
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			resp.Body.Close()
			if resp.StatusCode == http.StatusCreated {
				return nil
			}
			lastErr = fmt.Errorf("upload to storage: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
			if resp.StatusCode < 500 {
				return lastErr
			}
		} else {
			lastErr = fmt.Errorf("upload to storage: %w", err)
		}
		c.sleep(time.Duration(1<<attempt) * time.Second)
	}
	return lastErr
}
