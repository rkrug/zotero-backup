package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTagOrder(t *testing.T) {
	// ascending
	tags := []string{"v0.1.0-rc.1", "v0.1.0-rc.2", "v0.1.0-rc.10", "v0.1.0", "v0.2.0-alpha", "v0.2.0-alpha.1", "v0.2.0-beta", "v0.2.0", "v0.10.0", "v1.0.0"}
	for i := 1; i < len(tags); i++ {
		a, oka := parseTag(tags[i-1])
		b, okb := parseTag(tags[i])
		if !oka || !okb || !a.less(b) || b.less(a) {
			t.Errorf("%s < %s: %v %v %v %v", tags[i-1], tags[i], oka, okb, a.less(b), b.less(a))
		}
	}
	for _, bad := range []string{"0.2.0", "v0.2", "v0.2.0+build", "v0.2.0-", "v0.2.0-rc..1", "va.b.c", "dev"} {
		if _, ok := parseTag(bad); ok {
			t.Errorf("%q parsed as a version", bad)
		}
	}
}

func TestNewestRelease(t *testing.T) {
	rels := []ghRelease{{Tag: "v0.3.0", Draft: true}, {Tag: "v0.3.0-rc.1", Prerelease: true}, {Tag: "v0.2.1-rc.1"},
		{Tag: "v0.2.0"}, {Tag: "nightly"}, {Tag: "v0.2.5", Prerelease: true}}
	if r, _, _ := newestRelease(rels, false); r.Tag != "v0.2.0" {
		t.Errorf("stable: %s", r.Tag)
	}
	if r, _, _ := newestRelease(rels, true); r.Tag != "v0.3.0-rc.1" {
		t.Errorf("beta: %s", r.Tag)
	}
	if _, _, ok := newestRelease(nil, false); ok {
		t.Error("found a release in none")
	}
}

type fakeRel struct {
	tag        string
	prerelease bool
}

// fakeGitHub serves a release list and, per release, the archives for
// darwin/arm64 and windows/amd64 (each holding "binary <tag>"), SHA256SUMS
// and SHA256SUMS.sig signed with priv. change may alter the files before
// they are served.
func fakeGitHub(t *testing.T, rels []fakeRel, priv ed25519.PrivateKey, change func(files map[string][]byte)) *httptest.Server {
	t.Helper()
	files := map[string][]byte{}
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
	type rel struct {
		Tag        string  `json:"tag_name"`
		Prerelease bool    `json:"prerelease"`
		Assets     []asset `json:"assets"`
	}
	srv := httptest.NewServer(nil)
	var list []rel
	for _, r := range rels {
		var sums strings.Builder
		var assets []asset
		add := func(name string, data []byte) {
			files["/rkrug/zotero-backup/releases/download/"+r.tag+"/"+name] = data
			assets = append(assets, asset{name, srv.URL + "/rkrug/zotero-backup/releases/download/" + r.tag + "/" + name})
		}
		for _, sys := range []string{"darwin-arm64", "windows-amd64"} {
			dir := "zotero-backup-" + r.tag + "-" + sys
			bin := []byte("binary " + r.tag)
			var data []byte
			name := dir + ".tar.gz"
			if strings.HasPrefix(sys, "windows") {
				name, data = dir+".zip", zipFileOf(t, dir+"/zotero-backup.exe", bin)
			} else {
				data = tarGzOf(t, dir+"/zotero-backup", bin)
			}
			add(name, data)
			sum := sha256.Sum256(data)
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		}
		add("SHA256SUMS", []byte(sums.String()))
		add("SHA256SUMS.sig", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums.String())))+"\n"))
		list = append(list, rel{r.tag, r.prerelease, assets})
	}
	if change != nil {
		change(files)
	}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			json.NewEncoder(w).Encode(list)
			return
		}
		if b, ok := files[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	})
	t.Cleanup(srv.Close)
	return srv
}

func tarGzOf(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg})
	tw.Write(data)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipFileOf(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write(data)
	zw.Close()
	return buf.Bytes()
}

// testUpdater runs as version cur, binary "old" in a temporary folder,
// darwin/arm64; answer is what it answers when asked ("" with noTerm: off a
// terminal).
func testUpdater(t *testing.T, srv *httptest.Server, pub ed25519.PublicKey, cur, answer string, out *strings.Builder) *updater {
	exe := filepath.Join(t.TempDir(), "zotero-backup")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &updater{api: srv.URL + "/releases", download: srv.URL + "/rkrug/zotero-backup/releases/download/",
		http: srv.Client(), key: pub, current: cur, exe: exe, goos: "darwin", goarch: "arm64", out: out,
		runVersion: func(p string) (string, error) {
			b, err := os.ReadFile(p)
			return strings.Replace(string(b), "binary", "zotero-backup", 1), err
		},
		ask: func(q string) (string, error) {
			fmt.Fprintln(out, "ASK", q)
			if answer == "" {
				return "", errNoTerminal
			}
			return answer, nil
		}}
}

func exeContent(t *testing.T, u *updater) string {
	b, err := os.ReadFile(u.exe)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeChoice(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	rels := []fakeRel{{"v0.1.0", false}, {"v0.2.0-rc.1", true}, {"v0.2.0", false}, {"v0.3.0-rc.1", true}}
	srv := fakeGitHub(t, rels, priv, nil)
	cases := []struct {
		name, cur, channel, answer string
		check, yes, downgrade      bool
		installed, err, out        string // installed: binary afterwards; err, out: substrings
	}{
		{name: "up to date stable", cur: "v0.2.0", channel: "stable", installed: "old", out: "up to date on the stable channel"},
		{name: "newest", cur: "v0.3.0-rc.1", installed: "old", out: "is the newest release"},
		{name: "stable update with yes", cur: "v0.1.0", channel: "stable", yes: true, installed: "binary v0.2.0", out: "updated"},
		{name: "beta update with yes", cur: "v0.1.0", channel: "beta", yes: true, installed: "binary v0.3.0-rc.1"},
		{name: "asks, answer yes", cur: "v0.1.0", channel: "stable", answer: "y", installed: "binary v0.2.0", out: "ASK Update to v0.2.0 (stable)? [y/N]"},
		{name: "asks, answer no", cur: "v0.1.0", channel: "stable", answer: "n", installed: "old", err: "not updated"},
		{name: "both offered, choose 2", cur: "v0.1.0", answer: "2", installed: "binary v0.3.0-rc.1", out: "1) v0.2.0 (stable)  2) v0.3.0-rc.1 (beta)"},
		{name: "both offered, choose 1", cur: "v0.1.0", answer: "1", installed: "binary v0.2.0"},
		{name: "both offered, yes needs a channel", cur: "v0.1.0", yes: true, installed: "old", err: "name the channel"},
		{name: "off a terminal", cur: "v0.1.0", channel: "stable", installed: "old", err: "not on a terminal: update with zotero-backup upgrade stable --yes"},
		{name: "check", cur: "v0.1.0", check: true, installed: "old", err: "an update is available", out: "v0.3.0-rc.1 (beta)"},
		{name: "check up to date", cur: "v0.2.0", channel: "stable", check: true, installed: "old"},
		{name: "beta build, stable never downgrades", cur: "v0.2.0-rc.1", channel: "stable", yes: true, installed: "binary v0.2.0"},
		{name: "newest beta, stable older: nothing", cur: "v0.3.0-rc.1", channel: "stable", yes: true, installed: "old", out: "up to date"},
		{name: "downgrade", cur: "v0.3.0-rc.1", channel: "stable", downgrade: true, answer: "y", installed: "binary v0.2.0", out: "Go back from v0.3.0-rc.1 to v0.2.0 (stable)"},
		{name: "downgrade from stable refused", cur: "v0.2.0", channel: "stable", downgrade: true, installed: "old", err: "--downgrade only"},
		{name: "go install build refused", cur: "dev", channel: "stable", yes: true, installed: "old", err: "not a release build"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			u := testUpdater(t, srv, pub, c.cur, c.answer, &out)
			err := u.run(c.channel, c.check, c.yes, c.downgrade)
			if (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
				t.Errorf("error %v, want %q\n%s", err, c.err, out.String())
			}
			if got := exeContent(t, u); got != c.installed {
				t.Errorf("binary %q, want %q\n%s", got, c.installed, out.String())
			}
			if !strings.Contains(out.String(), c.out) {
				t.Errorf("output lacks %q:\n%s", c.out, out.String())
			}
		})
	}
}

func TestUpgradeRefusesBadDownloads(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	base := "/rkrug/zotero-backup/releases/download/v0.2.0/"
	archive := base + "zotero-backup-v0.2.0-darwin-arm64.tar.gz"
	cases := []struct {
		name   string
		key    ed25519.PublicKey
		change func(map[string][]byte)
		err    string
	}{
		{"no key in this build", nil, nil, "cannot verify updates"},
		{"signed with another key", otherPub, nil, "signature of SHA256SUMS is not valid"},
		{"SHA256SUMS changed", pub, func(f map[string][]byte) { f[base+"SHA256SUMS"] = append(f[base+"SHA256SUMS"], ' ') }, "signature of SHA256SUMS is not valid"},
		{"signature missing", pub, func(f map[string][]byte) { delete(f, base+"SHA256SUMS.sig") }, "HTTP 404"},
		{"archive changed", pub, func(f map[string][]byte) {
			f[archive] = tarGzOf(t, "zotero-backup-v0.2.0-darwin-arm64/zotero-backup", []byte("evil"))
		}, "does not match its checksum"},
		{"archive missing", pub, func(f map[string][]byte) { delete(f, archive) }, "HTTP 404"},
		{"wrong version inside", pub, func(f map[string][]byte) {
			data := tarGzOf(t, "zotero-backup-v0.2.0-darwin-arm64/zotero-backup", []byte("binary v0.1.0"))
			f[archive] = data
			sum := sha256.Sum256(data)
			sums := fmt.Sprintf("%s  zotero-backup-v0.2.0-darwin-arm64.tar.gz\n", hex.EncodeToString(sum[:]))
			f[base+"SHA256SUMS"] = []byte(sums)
			f[base+"SHA256SUMS.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums))))
		}, `reports "zotero-backup v0.1.0"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := fakeGitHub(t, []fakeRel{{"v0.2.0", false}}, priv, c.change)
			var out strings.Builder
			u := testUpdater(t, srv, c.key, "v0.1.0", "", &out)
			err := u.run("stable", false, true, false)
			if err == nil || !strings.Contains(err.Error(), c.err) || !strings.Contains(err.Error(), "nothing was replaced") {
				t.Errorf("error %v, want %q", err, c.err)
			}
			if got := exeContent(t, u); got != "old" {
				t.Errorf("binary replaced: %q", got)
			}
			if entries, _ := os.ReadDir(filepath.Dir(u.exe)); len(entries) != 1 {
				t.Errorf("left files behind: %v", entries)
			}
		})
	}
}

func TestUpgradeAssetAddress(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	srv := fakeGitHub(t, []fakeRel{{"v0.2.0", false}}, priv, nil)
	var out strings.Builder
	u := testUpdater(t, srv, pub, "v0.1.0", "", &out)
	u.download = "https://github.com/rkrug/zotero-backup/releases/download/" // the fake's addresses are not this repository's
	err := u.run("stable", false, true, false)
	if err == nil || !strings.Contains(err.Error(), "unexpected address") {
		t.Errorf("error %v", err)
	}
}

func TestUpgradeWindows(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	srv := fakeGitHub(t, []fakeRel{{"v0.2.0", false}}, priv, nil)
	var out strings.Builder
	u := testUpdater(t, srv, pub, "v0.1.0", "", &out)
	u.goos, u.goarch = "windows", "amd64"
	u.exe += ".exe"
	os.WriteFile(u.exe, []byte("old"), 0o755)
	if err := u.run("stable", false, true, false); err != nil {
		t.Fatal(err, out.String())
	}
	if got := exeContent(t, u); got != "binary v0.2.0" {
		t.Errorf("binary %q", got)
	}
	if b, err := os.ReadFile(u.exe + ".old"); err != nil || string(b) != "old" {
		t.Errorf("old binary not kept as .old: %q %v", b, err)
	}
}

func TestUpgradeUnwritableFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("folder permissions")
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	srv := fakeGitHub(t, []fakeRel{{"v0.2.0", false}}, priv, nil)
	var out strings.Builder
	u := testUpdater(t, srv, pub, "v0.1.0", "", &out)
	dir := filepath.Dir(u.exe)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	err := u.run("stable", false, true, false)
	if err == nil || !strings.Contains(err.Error(), "cannot write to") || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error %v", err)
	}
	if got := exeContent(t, u); got != "old" {
		t.Errorf("binary %q", got)
	}
}

func TestReplaceExeWindowsRollsBack(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "zotero-backup.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	err := replaceExe(exe, filepath.Join(dir, "missing"), true)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Errorf("old binary not put back: %q", b)
	}
}
