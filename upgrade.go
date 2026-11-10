package main

// upgrade: zotero-backup replaces itself with a release from GitHub (#18), as the
// in-app updaters of parquet_indexer and GD Explorer do. The release workflow
// signs SHA256SUMS with an Ed25519 key (tools/sign.go); the public key is built
// in. upgrade downloads the archive for this system, SHA256SUMS and its
// signature from this repository's release only, checks the signature, then the
// archive's sha256, unpacks only the binary next to the running one, checks that
// it reports the expected version, and renames it over the running binary.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	releasesAPI  = "https://api.github.com/repos/rkrug/zotero-backup/releases"
	releasesPage = "https://github.com/rkrug/zotero-backup/releases"
	downloadBase = "https://github.com/rkrug/zotero-backup/releases/download/"
)

// updatePublicKey checks SHA256SUMS.sig: a raw Ed25519 public key, base64
// (go run tools/sign.go keygen). Empty: this build cannot verify updates and
// only points to the release page.
const updatePublicKey = ""

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// semver is a release tag vX.Y.Z[-pre], ordered as in SemVer: a pre-release
// comes before its release; pre-release parts compare numerically or as text.
type semver struct {
	n   [3]int
	pre []string
}

func parseTag(tag string) (semver, bool) {
	var v semver
	s, ok := strings.CutPrefix(tag, "v")
	if !ok || strings.Contains(s, "+") {
		return v, false
	}
	s, pre, hasPre := strings.Cut(s, "-")
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, p := range v.pre {
			if p == "" {
				return v, false
			}
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

func (a semver) less(b semver) bool {
	if a.n != b.n {
		for i := range a.n {
			if a.n[i] != b.n[i] {
				return a.n[i] < b.n[i]
			}
		}
	}
	if len(a.pre) == 0 || len(b.pre) == 0 {
		return len(a.pre) > 0 && len(b.pre) == 0
	}
	for i := 0; i < min(len(a.pre), len(b.pre)); i++ {
		x, y := a.pre[i], b.pre[i]
		if x == y {
			continue
		}
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			return xn < yn
		case xerr == nil:
			return true
		case yerr == nil:
			return false
		}
		return x < y
	}
	return len(a.pre) < len(b.pre)
}

// newestRelease is the newest release on a channel: stable leaves out
// pre-releases (by GitHub's flag and by the tag), beta takes both. Drafts and
// tags that are no version are skipped.
func newestRelease(rels []ghRelease, beta bool) (ghRelease, semver, bool) {
	var best ghRelease
	var bv semver
	found := false
	for _, r := range rels {
		v, ok := parseTag(r.Tag)
		if !ok || r.Draft || (!beta && (r.Prerelease || len(v.pre) > 0)) {
			continue
		}
		if !found || bv.less(v) {
			best, bv, found = r, v, true
		}
	}
	return best, bv, found
}

// updater does the work of upgrade; its fields are replaced in tests.
type updater struct {
	api, download string // releasesAPI, downloadBase
	http          *http.Client
	key           ed25519.PublicKey // nil: cannot verify updates
	current       string            // this binary's version
	exe           string            // path of this binary
	goos, goarch  string
	runVersion    func(path string) (string, error) // output of "<path> version"
	ask           func(question string) (string, error)
	out           io.Writer
}

func newUpdater(out io.Writer) (*updater, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot find this program's file: %w", err)
	}
	var key ed25519.PublicKey
	if updatePublicKey != "" {
		key, _ = base64.StdEncoding.DecodeString(updatePublicKey)
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 5 {
			return fmt.Errorf("refused redirect to %s", req.URL)
		}
		return nil
	}}
	return &updater{api: releasesAPI, download: downloadBase, http: client, key: key, current: version, exe: exe,
		goos: runtime.GOOS, goarch: runtime.GOARCH, runVersion: runVersion, ask: askLine, out: out}, nil
}

func runVersion(path string) (string, error) {
	b, err := exec.Command(path, "version").Output()
	return strings.TrimSpace(string(b)), err
}

var errNoTerminal = errors.New("not on a terminal")

// askLine asks on the terminal and returns the answer; off a terminal it fails.
func askLine(question string) (string, error) {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return "", errNoTerminal
	}
	fmt.Fprint(os.Stderr, question+" ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line)), nil
}

type offer struct {
	rel     ghRelease
	v       semver
	channel string
}

func (o offer) label() string { return o.rel.Tag + " (" + o.channel + ")" }

// run is the upgrade command; channel is "", "stable" or "beta".
func (u *updater) run(channel string, check, yes, downgrade bool) error {
	cur, ok := parseTag(u.current)
	if !ok {
		return fmt.Errorf("zotero-backup %s is not a release build (go install or a build of your own): update it the same way, e.g. go install github.com/rkrug/zotero-backup@latest", u.current)
	}
	if strings.Contains(filepath.ToSlash(u.exe), "/Cellar/") {
		return errors.New("installed with Homebrew: update it with brew upgrade zotero-backup")
	}
	if downgrade && (channel != "stable" || len(cur.pre) == 0) {
		return errors.New("--downgrade only goes from a pre-release back to a stable release: zotero-backup upgrade stable --downgrade")
	}
	rels, err := u.releases()
	if err != nil {
		return err
	}
	stable, sv, hasStable := newestRelease(rels, false)
	beta, bv, hasBeta := newestRelease(rels, true)

	fmt.Fprintf(u.out, "running         %s\n", u.current)
	if hasStable {
		fmt.Fprintf(u.out, "newest stable   %s\n", stable.Tag)
	}
	if hasBeta && len(bv.pre) > 0 && (!hasStable || sv.less(bv)) {
		fmt.Fprintf(u.out, "newest beta     %s\n", beta.Tag)
	}

	var offers []offer
	if channel != "beta" && hasStable && (cur.less(sv) || (downgrade && sv.less(cur))) {
		offers = append(offers, offer{stable, sv, "stable"})
	}
	if channel != "stable" && hasBeta && cur.less(bv) && len(bv.pre) > 0 {
		offers = append(offers, offer{beta, bv, "beta"})
	}
	if len(offers) == 0 {
		switch {
		case downgrade:
			fmt.Fprintln(u.out, "No stable release to go back to.")
		case channel != "":
			fmt.Fprintf(u.out, "zotero-backup %s is up to date on the %s channel.\n", u.current, channel)
		default:
			fmt.Fprintf(u.out, "zotero-backup %s is the newest release.\n", u.current)
		}
		return nil
	}
	for _, o := range offers {
		fmt.Fprintf(u.out, "%-15s %s\n", o.label(), releasesPage+"/tag/"+o.rel.Tag)
	}
	if check {
		return fmt.Errorf("an update is available: zotero-backup upgrade %s", offers[0].channel)
	}

	var o offer
	switch {
	case yes && len(offers) == 1:
		o = offers[0]
	case yes:
		return errors.New("name the channel: zotero-backup upgrade stable --yes, or upgrade beta --yes")
	default:
		q := fmt.Sprintf("Update to %s? [y/N]", offers[0].label())
		if offers[0].v.less(cur) {
			q = fmt.Sprintf("Go back from %s to %s? [y/N]", u.current, offers[0].label())
		}
		if len(offers) == 2 {
			q = fmt.Sprintf("1) %s  2) %s  [1/2/N]", offers[0].label(), offers[1].label())
		}
		a, err := u.ask(q)
		if errors.Is(err, errNoTerminal) {
			return fmt.Errorf("not on a terminal: update with zotero-backup upgrade %s --yes", offers[0].channel)
		} else if err != nil {
			return err
		}
		switch {
		case len(offers) == 1 && (a == "y" || a == "yes"), len(offers) == 2 && a == "1":
			o = offers[0]
		case len(offers) == 2 && a == "2":
			o = offers[1]
		default:
			return errors.New("not updated")
		}
	}
	return u.install(o.rel)
}

func (u *updater) releases() ([]ghRelease, error) {
	b, err := u.fetch(u.api+"?per_page=100", 10<<20)
	if err != nil {
		return nil, fmt.Errorf("reading the releases: %w", err)
	}
	var rels []ghRelease
	if err := json.Unmarshal(b, &rels); err != nil {
		return nil, fmt.Errorf("reading the releases: %w", err)
	}
	return rels, nil
}

func (u *updater) fetch(url string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zotero-backup/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("GET %s: larger than %d bytes", url, limit)
	}
	return b, err
}

// install downloads, checks and puts release r in place of the running binary.
// Nothing is replaced unless every check passed.
func (u *updater) install(r ghRelease) error {
	page := releasesPage + "/tag/" + r.Tag
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%s; nothing was replaced. Download it from %s", fmt.Sprintf(format, a...), page)
	}
	if u.key == nil {
		return fail("this build cannot verify updates")
	}
	ext, bin := ".tar.gz", "zotero-backup"
	if u.goos == "windows" {
		ext, bin = ".zip", "zotero-backup.exe"
	}
	dir := "zotero-backup-" + r.Tag + "-" + u.goos + "-" + u.goarch
	name := dir + ext
	asset := func(n string) (string, error) {
		want := u.download + r.Tag + "/" + n
		for _, a := range r.Assets {
			if a.Name == n {
				if a.URL != want {
					return "", fail("%s has an unexpected address: %s", n, a.URL)
				}
				return want, nil
			}
		}
		return "", fail("the release has no %s", n)
	}
	var urls [3]string
	for i, n := range []string{name, "SHA256SUMS", "SHA256SUMS.sig"} {
		var err error
		if urls[i], err = asset(n); err != nil {
			return err
		}
	}
	sums, err := u.fetch(urls[1], 1<<20)
	if err != nil {
		return fail("%v", err)
	}
	sig, err := u.fetch(urls[2], 1<<10)
	if err != nil {
		return fail("%v", err)
	}
	if s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig))); err != nil || !ed25519.Verify(u.key, sums, s) {
		return fail("the signature of SHA256SUMS is not valid")
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
		}
	}
	if want == "" {
		return fail("SHA256SUMS has no line for %s", name)
	}
	fmt.Fprintf(u.out, "downloading     %s\n", name)
	archive, err := u.fetch(urls[0], 200<<20)
	if err != nil {
		return fail("%v", err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return fail("%s does not match its checksum", name)
	}
	data, err := extract(archive, ext, dir+"/"+bin)
	if err != nil {
		return fail("%v", err)
	}

	pattern := ".zotero-backup-new-*"
	if u.goos == "windows" {
		pattern += ".exe"
	}
	f, err := os.CreateTemp(filepath.Dir(u.exe), pattern)
	if err != nil {
		hint := "run it again with sudo: sudo " + u.exe + " upgrade"
		if u.goos == "windows" {
			hint = "run it again as administrator"
		}
		return fail("cannot write to %s (%v): %s", filepath.Dir(u.exe), err, hint)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o755)
	}
	if err != nil {
		os.Remove(tmp)
		return fail("writing the new version: %v", err)
	}
	if got, err := u.runVersion(tmp); err != nil || got != "zotero-backup "+r.Tag {
		os.Remove(tmp)
		return fail("the new file reports %q (%v), not zotero-backup %s", got, err, r.Tag)
	}
	if err := replaceExe(u.exe, tmp, u.goos == "windows"); err != nil {
		os.Remove(tmp)
		return fail("replacing %s: %v", u.exe, err)
	}
	fmt.Fprintf(u.out, "updated         %s → %s (%s)\n", u.current, r.Tag, u.exe)
	return nil
}

// extract returns the file at path inside a .tar.gz or .zip archive.
func extract(archive []byte, ext, path string) ([]byte, error) {
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == path {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 200<<20))
			}
		}
		return nil, fmt.Errorf("%s is not in the archive", path)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s is not in the archive", path)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == path && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// replaceExe puts the file newPath in place of exe. A running .exe on Windows
// cannot be overwritten but can be renamed: it becomes exe.old, removed by the
// next start (removeOldExe).
func replaceExe(exe, newPath string, windows bool) error {
	if !windows {
		return os.Rename(newPath, exe)
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}

// removeOldExe removes the binary an upgrade on Windows left behind.
func removeOldExe() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
