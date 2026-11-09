package main

// `zotero-backup stresstest`: an end-to-end test of backup and restore against
// the real Zotero server AND a running Zotero desktop (through the Zotero Debug
// Bridge plugin), with local changes, syncs, deletions, the trash and Zotero's
// conflict window. It deliberately changes the libraries it runs on, so it is
// left out of the help text, needs every library named explicitly, and asks
// before it touches each one. See docs/stresstest.md.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const stressUsage = `usage: zotero-backup stresstest --library LIB [--library LIB …] [options]

Tests backup and restore against the real Zotero server and a running Zotero
desktop: local changes, syncs, deletions, the trash, conflicts. It CHANGES the
libraries it runs on: run it ONLY on test groups or libraries, ideally of a
separate Zotero account just for testing. Details: docs/stresstest.md.

Steps:

  1. Zotero: running, with the Zotero Debug Bridge plugin (token set in its
     settings), automatic sync off (Settings → Sync), and every library under
     test synced completely.

  2. A write key with write access ONLY to the libraries under test (created at
     zotero.org/settings/keys); the test refuses a key that could write anywhere
     else. Store it, and optionally a read-only key, in the keychain:

       zotero-backup key set zb-test-write
       zotero-backup key set zb-test-read

  3. A profile in the configuration file (zotero-backup help shows its path):

       "test": { "key": "zb-test-read", "write_key": "zb-test-write", "to": "~/zotero-backups" }

  4. A short run first, then everything. The test asks before it touches each
     library; answer y only for a test library (Enter = no):

       zotero-backup stresstest --profile test --library group:<id> --scenarios desktop-synced --ops edit-title,erase-item,replace-file
       zotero-backup stresstest --profile test --library group:<id> [--library user]

  5. The report: <to>/stresstest/<run>/report.html (to read) and report.json
     (for further processing). The command fails if any check failed.

Options:

  --library LIB          group:<id> or user (the key's personal library); repeat for several
  --scenarios A,B        desktop-synced, api, unsynced-remote, unsynced-local, unsynced-only, extras (default: all)
  --ops A,B              edit-title, fill-extra, trash-item, erase-item, erase-note, erase-attachment,
                         erase-annotation, edit-note, replace-file, trash-collection, erase-collection,
                         move-membership, tags, tag-colour, new-item (default: all)
  --report DIR           where report.html and report.json go (default: <to>/stresstest/<run>)
  --keep                 keep the test objects at the end
  --zotero-profile P     Zotero profile (name or folder) whose Debug Bridge token to use
                         (default: the default profile in Zotero's profiles.ini)
  --profile NAME         zotero-backup profile with the keys (write_key, else key) and the folder (to)
`

type stressScenario struct {
	name, desc, expect string
}

var stressScenarios = []stressScenario{
	{"desktop-synced",
		"change in Zotero desktop, synced; restore; desktop syncs",
		"server and desktop back to the backup, no conflict"},
	{"api",
		"change on the server by another client (API); restore; desktop syncs",
		"server and desktop back to the backup, no conflict"},
	{"unsynced-remote",
		"change in desktop, synced; a second change in desktop NOT synced; restore; desktop syncs choosing the server's version in the conflict window",
		"server and desktop back to the backup"},
	{"unsynced-local",
		"as unsynced-remote, but the conflict window chooses the local version",
		"the local version wins (whole object, both changes) and undoes the restore; a second restore after syncing brings everything back"},
	{"unsynced-only",
		"change in desktop NOT synced at all; restore; desktop syncs",
		"restore finds nothing to do (the change is not on the server); after the sync the server has the change; a second restore brings it back"},
}

// stress is the run against one library; the libraries share the report.
type stress struct {
	pr        *progress
	run       string
	c         *Client // write key
	readOnly  *Client // a key that may not write, for the refusal check (nil: skipped)
	lib       library
	api       serverOps
	z         *bridge
	root      string // the run's backups
	profile   Profile
	rep       *stressReport
	scenarios []string
	ops       []string
	keep      bool
}

func (s *stress) record(sc, op, check string, ok bool, detail string, args ...any) {
	res := "pass"
	if !ok {
		res = "fail"
	}
	d := fmt.Sprintf(detail, args...)
	if ok && (d == "<nil>" || d == "[]" || d == "<nil> map[]" || d == "<nil>, 0 resumed") {
		d = ""
	}
	s.rep.add(stressResult{s.lib.ID, s.lib.Name, sc, op, check, res, d, time.Now().UTC()})
	if !ok {
		s.pr.Warnf("FAILED %s / %s / %s / %s: %s", s.lib.Label(), sc, op, check, d)
	}
}

func (s *stress) note(sc, op, check, detail string, args ...any) {
	s.rep.add(stressResult{s.lib.ID, s.lib.Name, sc, op, check, "info", fmt.Sprintf(detail, args...), time.Now().UTC()})
}

func (s *stress) selectedOps() []stressOp {
	var out []stressOp
	for _, o := range stressOps {
		if len(s.ops) == 0 || slices.Contains(s.ops, o.name) {
			out = append(out, o)
		}
	}
	return out
}

// fixedClient gives the same client for every library.
func fixedClient(c *Client) func(string) (*Client, error) {
	return func(string) (*Client, error) { return c, nil }
}

type stressOptions struct {
	libraries     []string
	scenarios     []string
	ops           []string
	report        string
	keep          bool
	zoteroProfile string
}

// stressTarget is one library, checked and confirmed before anything changes.
type stressTarget struct {
	lib      library
	c        *Client
	readOnly *Client
	z        *bridge
	items    int
	dataDir  string
}

// runStresstest checks every library, asks for each, then runs the scenarios
// library by library and writes the reports. It fails if any check failed.
func runStresstest(p Profile, o stressOptions, in *os.File, out io.Writer, pr *progress) error {
	if len(o.libraries) == 0 {
		fmt.Fprint(out, stressUsage)
		return errors.New("stresstest needs at least one --library (a test group or library)")
	}
	for _, sc := range o.scenarios {
		if sc != "extras" && !slices.ContainsFunc(stressScenarios, func(x stressScenario) bool { return x.name == sc }) {
			return fmt.Errorf("unknown scenario %q", sc)
		}
	}
	for _, op := range o.ops {
		if !slices.ContainsFunc(stressOps, func(x stressOp) bool { return x.name == op }) {
			return fmt.Errorf("unknown operation %q", op)
		}
	}
	profileDir, err := findZoteroProfile(o.zoteroProfile)
	if err != nil {
		return err
	}

	// check every library before anything changes
	var targets []stressTarget
	for _, spec := range o.libraries {
		t, err := stressCheck(p, spec, profileDir)
		if err != nil {
			return fmt.Errorf("%s: %w", spec, err)
		}
		targets = append(targets, t)
	}
	// least privilege: no write key may reach any library that was not named
	named := map[string]bool{}
	for _, t := range targets {
		named[t.lib.ID] = true
	}
	checked := map[*Client]bool{}
	for _, t := range targets {
		if checked[t.c] {
			continue
		}
		checked[t.c] = true
		if err := checkKeyScope(t.c, named); err != nil {
			return err
		}
	}

	for _, t := range targets {
		kind := "group"
		if t.lib.Spec() == "user" {
			kind = "personal library"
		}
		ok, err := confirmStress(in, out, fmt.Sprintf("%q (%s %s, %d items on zotero.org; Zotero data folder %s)",
			t.lib.Name, kind, strings.TrimPrefix(strings.TrimPrefix(t.lib.ID, "group-"), "user-"), t.items, t.dataDir))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not run")
		}
	}

	run := time.Now().UTC().Format("20060102T150405")
	root := filepath.Join(expandHome(p.To), "stresstest", run)
	reportDir := root
	if o.report != "" {
		reportDir = expandHome(o.report)
	}
	rep := &stressReport{Run: run, Started: time.Now().UTC(), Tool: version, Backups: root,
		Scenarios: o.scenarios, Operations: o.ops, System: runtime.GOOS + "/" + runtime.GOARCH}
	defer func() {
		rep.Finished = time.Now().UTC()
		if err := rep.write(reportDir); err != nil {
			pr.Warnf("report: %v", err)
		} else {
			fmt.Fprintf(out, "report: %s, %s\n", filepath.Join(reportDir, "report.html"), filepath.Join(reportDir, "report.json"))
		}
	}()

	for _, t := range targets {
		s := &stress{pr: pr, run: run, c: t.c, readOnly: t.readOnly, lib: t.lib, api: serverOps{t.c, t.lib.Path}, z: t.z,
			root: root, profile: p, rep: rep, scenarios: o.scenarios, ops: o.ops, keep: o.keep}
		s.profile.To = root
		pr.SetPrefix(t.lib.Dir())
		if err := s.runLibrary(expandHome(p.To)); err != nil {
			s.record("setup", "*", "run", false, "%v", err)
		}
	}
	sum := rep.summary()
	fmt.Fprintf(out, "%d checks: %d passed, %d failed, %d observations\n", sum.Checks, sum.Passed, sum.Failed, sum.Info)
	if sum.Failed > 0 {
		return fmt.Errorf("%d checks failed", sum.Failed)
	}
	return nil
}

// stressCheck resolves a library and checks the keys and the desktop.
func stressCheck(p Profile, spec, profileDir string) (stressTarget, error) {
	var t stressTarget
	kind, gid, err := parseLibrary(spec)
	if err != nil || kind == "all" {
		return t, errors.New(`name each library: group:<id> or user ("all" is not accepted)`)
	}
	if t.c, err = clientsFor(p.forRestore(), newClient)(spec); err != nil {
		return t, err
	}
	libs, _, err := resolveLibraries(t.c, spec)
	if err != nil {
		return t, err
	}
	t.lib = libs[0]
	if err := canWrite(t.c, t.lib.Path); err != nil {
		return t, fmt.Errorf("%w (the stress test uses the profile's write_key, else its key)", err)
	}
	if rc, err := clientsFor(p, newClient)(spec); err == nil && canWrite(rc, t.lib.Path) != nil {
		t.readOnly = rc
	}
	_, h, err := t.c.get(t.lib.Path + "/items?limit=1&includeTrashed=1")
	if err != nil {
		return t, err
	}
	t.items, _ = strconv.Atoi(h.Get("Total-Results"))

	id, name := gid, t.lib.Name
	if kind == "user" {
		k, err := t.c.keyInfo()
		if err != nil {
			return t, err
		}
		id, name = k.UserID, k.Username
	}
	if t.z, err = newBridge(profileDir, kind, id, name); err != nil {
		return t, err
	}
	var pre struct {
		Zotero, DataDir                                    string
		LibraryID                                          int
		Editable                                           bool
		LibraryVersion, UnsyncedItems, UnsyncedCollections int
	}
	if err := t.z.run(jsPreflight, nil, &pre); err != nil {
		return t, err
	}
	t.dataDir = pre.DataDir
	if !pre.Editable || pre.LibraryVersion == 0 || pre.UnsyncedItems+pre.UnsyncedCollections > 0 {
		return t, fmt.Errorf("Zotero desktop is not ready for this library: editable %v, library version %d, %d unsynced items, %d unsynced collections (sync it completely first)",
			pre.Editable, pre.LibraryVersion, pre.UnsyncedItems, pre.UnsyncedCollections)
	}
	return t, nil
}

// confirmStress asks on a terminal whether lib is a test library; the answer
// defaults to no. Off a terminal it refuses: there is no way around asking.
func confirmStress(in *os.File, out io.Writer, lib string) (bool, error) {
	if st, err := in.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false, errors.New("the stress test asks before it changes a library, and needs a terminal for that")
	}
	return askStress(bufio.NewReader(in), out, lib), nil
}

func askStress(in *bufio.Reader, out io.Writer, lib string) bool {
	fmt.Fprintf(out, "\nThis test is altering the content of the group or library.\n"+
		"DO ONLY RUN IT ON A TEST GROUP OR LIBRARY!\n"+
		"Are you sure that %s is a test group or library, and you want to run the test there? [N/y] ", lib)
	line, _ := in.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// runLibrary syncs the desktop, makes the safety backup, runs the scenarios and
// the extras, and cleans up.
func (s *stress) runLibrary(safetyRoot string) error {
	s.pr.Logf("initial sync")
	if r, err := s.z.sync("fail"); err != nil || len(r.Conflicts) > 0 {
		return fmt.Errorf("initial sync: %v %v", err, r.Conflicts)
	}
	s.pr.Logf("safety backup into %s", safetyRoot)
	safetyDir, _, err := backupLibrary(s.c, s.lib, safetyRoot, nil, time.Now(), quietProgress())
	if err != nil {
		return fmt.Errorf("safety backup: %w", err)
	}
	s.rep.Libraries = append(s.rep.Libraries, stressLibrary{s.lib.ID, s.lib.Name, safetyDir})
	defer s.finish(safetyDir)

	for _, sc := range stressScenarios {
		if len(s.scenarios) > 0 && !slices.Contains(s.scenarios, sc.name) {
			continue
		}
		s.pr.Logf("scenario %s", sc.name)
		if err := s.scenario(sc); err != nil {
			s.record(sc.name, "*", "setup", false, "%v", err)
		}
	}
	if len(s.scenarios) == 0 || slices.Contains(s.scenarios, "extras") {
		s.pr.Logf("extras")
		if err := s.extras(); err != nil {
			s.record("extras", "*", "setup", false, "%v", err)
		}
	}
	return nil
}

// zoteroDir is where Zotero keeps profiles.ini.
func zoteroDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Zotero")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Zotero", "Zotero")
	}
	return filepath.Join(home, ".zotero", "zotero")
}

// findZoteroProfile returns a Zotero profile folder: a folder given directly,
// a profile name from profiles.ini, or (empty) the default profile.
func findZoteroProfile(want string) (string, error) {
	if want != "" {
		if st, err := os.Stat(expandHome(want)); err == nil && st.IsDir() {
			return expandHome(want), nil
		}
	}
	dir := zoteroDir()
	b, err := os.ReadFile(filepath.Join(dir, "profiles.ini"))
	if err != nil {
		return "", fmt.Errorf("Zotero profiles: %w (give --zotero-profile FOLDER)", err)
	}
	return parseProfilesINI(string(b), dir, want)
}

func parseProfilesINI(ini, dir, want string) (string, error) {
	type prof struct {
		name, path string
		relative   bool
		def        bool
	}
	var all []prof
	var cur *prof
	for _, line := range strings.Split(ini, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[Profile"):
			all = append(all, prof{})
			cur = &all[len(all)-1]
		case strings.HasPrefix(line, "["):
			cur = nil
		case cur != nil:
			k, v, _ := strings.Cut(line, "=")
			switch k {
			case "Name":
				cur.name = v
			case "Path":
				cur.path = v
			case "IsRelative":
				cur.relative = v == "1"
			case "Default":
				cur.def = v == "1"
			}
		}
	}
	for _, p := range all {
		if (want == "" && p.def) || (want != "" && p.name == want) {
			if p.relative {
				return filepath.Join(dir, filepath.FromSlash(p.path)), nil
			}
			return p.path, nil
		}
	}
	if want == "" {
		return "", errors.New("no default Zotero profile in profiles.ini (give --zotero-profile)")
	}
	return "", fmt.Errorf("no Zotero profile named %q in profiles.ini", want)
}

func (s *stress) backup() (*backupSet, error) {
	time.Sleep(1100 * time.Millisecond) // backup folders are named by the second
	dir, _, err := backupLibrary(s.c, s.lib, s.root, nil, time.Now(), quietProgress())
	if err != nil {
		return nil, err
	}
	return loadBackup(dir)
}

func (s *stress) restore(sc string, b *backupSet, step string) {
	var out strings.Builder
	err := runRestore(fixedClient(s.c), b.dir, s.profile, nil, false, true, &out, quietProgress())
	s.record(sc, "*", step, err == nil, "%v", err)
	if err != nil {
		s.pr.Logf("%s", out.String())
	}
}

func (s *stress) sync(sc, step, policy string) syncResult {
	r, err := s.z.sync(policy)
	s.record(sc, "*", step, err == nil, "%v", err)
	s.record(sc, "*", step+": nothing left unsynced", r.UnsyncedItems+r.UnsyncedCollections == 0,
		"%d items, %d collections unsynced", r.UnsyncedItems, r.UnsyncedCollections)
	return r
}

func tagColour(settings map[string]json.RawMessage, name string) string {
	var s struct {
		Value []struct{ Name, Color string } `json:"value"`
	}
	json.Unmarshal(settings["tagColors"], &s)
	for _, v := range s.Value {
		if v.Name == name {
			return v.Color
		}
	}
	return ""
}

// failAfter lets the first n file downloads through, then fails every further
// one like a dropped connection.
type failAfter struct {
	n, seen int
}

func (f *failAfter) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/file") {
		if f.seen++; f.seen > f.n {
			return nil, errors.New("connection reset by peer (simulated)")
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (s *stress) families(tag string, list []stressOp) ([]family, error) {
	var names []string
	for _, o := range list {
		names = append(names, o.name)
	}
	var fams []family
	err := s.z.run(jsMakeFamilies, map[string]string{"run": s.run + "-" + tag, "ops": strings.Join(names, ",")}, &fams)
	return fams, err
}

// scenario runs all operations, each on its own fixture family, through one
// backup / change / restore / sync cycle.
func (s *stress) scenario(sc stressScenario) error {
	list := s.selectedOps()
	fams, err := s.families(sc.name, list)
	if err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}
	s.sync(sc.name, "sync fixtures", "fail")

	s.pr.Logf("%s: backup", sc.name)
	b0, err := s.backup()
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	s.checkBackupHasFixtures(sc.name, b0, fams)

	// apply the operations
	s.pr.Logf("%s: changing %d fixture families", sc.name, len(fams))
	for i, o := range list {
		f := &fams[i]
		var err error
		if sc.name == "api" {
			err = o.api(s.api, f)
		} else {
			var res struct {
				New string `json:"new"`
			}
			err = s.z.run(o.desktop, map[string]string{"family": f.json(), "run": f.Run}, &res)
			f.New = res.New
		}
		s.record(sc.name, o.name, "apply", err == nil, "%v", err)
	}
	if sc.name == "desktop-synced" || strings.HasPrefix(sc.name, "unsynced-") && sc.name != "unsynced-only" {
		s.sync(sc.name, "sync the changes", "fail")
	}
	touched := map[string]string{}
	if sc.name == "unsynced-remote" || sc.name == "unsynced-local" {
		for _, f := range fams {
			var k *string
			err := s.z.run(jsTouch, map[string]string{"family": f.json()}, &k)
			s.record(sc.name, f.Op, "second change, not synced", err == nil, "%v", err)
			if k != nil {
				touched[f.Op] = *k
			}
		}
	}

	// restore and sync the desktop
	s.pr.Logf("%s: restore", sc.name)
	s.restore(sc.name, b0, "restore")
	switch sc.name {
	case "unsynced-only":
		r := s.sync(sc.name, "sync after restore", "fail")
		s.note(sc.name, "*", "conflicts", "%d", len(r.Conflicts))
		s.checkAll(sc.name, "after the first restore", b0, fams, "changed")
		s.restore(sc.name, b0, "second restore")
		s.sync(sc.name, "sync after second restore", "remote")
		s.checkAll(sc.name, "after the second restore", b0, fams, "backup")
	case "unsynced-local":
		r := s.sync(sc.name, "sync after restore (choose local)", "local")
		s.note(sc.name, "*", "conflicts", "%d: %v", len(r.Conflicts), r.Conflicts)
		for _, f := range fams {
			if touched[f.Op] != "" {
				now, _ := readLibrary(s.c, s.lib.Path)
				p := makePlan(b0, now, nil, presentKeys(b0, f))
				s.note(sc.name, f.Op, "local version won", "%v (%d objects differ from the backup)", !p.empty(), len(p.objects))
			}
		}
		s.restore(sc.name, b0, "second restore")
		s.sync(sc.name, "sync after second restore", "remote")
		s.checkAll(sc.name, "after the second restore", b0, fams, "backup")
	case "unsynced-remote":
		r := s.sync(sc.name, "sync after restore (choose server)", "remote")
		s.note(sc.name, "*", "conflicts", "%d: %v", len(r.Conflicts), r.Conflicts)
		s.checkAll(sc.name, "after restore", b0, fams, "backup")
	default:
		r := s.sync(sc.name, "sync after restore", "fail")
		s.record(sc.name, "*", "no conflict", len(r.Conflicts) == 0, "%v", r.Conflicts)
		s.checkAll(sc.name, "after restore", b0, fams, "backup")
	}

	// a backup after the restore: the same as the first, unchanged files hard-linked
	b1, err := s.backup()
	if err != nil {
		s.record(sc.name, "*", "backup after restore", false, "%v", err)
		return nil
	}
	for _, f := range fams {
		for _, k := range []string{f.File, f.PDF} {
			x, okx := b0.files[k]
			y, oky := b1.files[k]
			if !okx || !oky || x.MD5 != y.MD5 {
				continue
			}
			sx, _ := os.Stat(filepath.Join(b0.dir, filepath.FromSlash(x.Path)))
			sy, _ := os.Stat(filepath.Join(b1.dir, filepath.FromSlash(y.Path)))
			s.record(sc.name, f.Op, "file "+k+" hard-linked in the next backup", sx != nil && sy != nil && os.SameFile(sx, sy), "")
		}
	}
	return nil
}

// presentKeys are the family's item keys that exist in the backup.
func presentKeys(b *backupSet, f family) []string {
	var out []string
	for _, k := range f.itemKeys() {
		if b.items[k] != nil {
			out = append(out, k)
		}
	}
	return out
}

func (s *stress) checkBackupHasFixtures(sc string, b *backupSet, fams []family) {
	for _, f := range fams {
		missing := []string{}
		for _, k := range []string{f.Parent, f.Note, f.File, f.PDF, f.Annotation} {
			if b.items[k] == nil {
				missing = append(missing, k)
			}
		}
		for _, k := range []string{f.Coll, f.Sub} {
			if b.collections[k] == nil {
				missing = append(missing, k)
			}
		}
		for _, k := range []string{f.File, f.PDF} {
			if _, ok := b.files[k]; !ok {
				missing = append(missing, "file "+k)
			}
		}
		s.record(sc, f.Op, "backup has the fixture (items, collections, files)", len(missing) == 0, "missing %v", missing)
	}
}

// checkAll compares, per family: the server with the backup ("backup": must
// match; "changed": must differ), the desktop with the server, and the tag colour.
func (s *stress) checkAll(sc, when string, b *backupSet, fams []family, want string) {
	now, err := readLibrary(s.c, s.lib.Path)
	if err != nil {
		s.record(sc, "*", when+": read library", false, "%v", err)
		return
	}
	for _, f := range fams {
		p := makePlan(b, now, nil, presentKeys(b, f))
		var diffs []string
		for _, ch := range p.objects {
			diffs = append(diffs, fmt.Sprintf("%s %s %s %v", ch.Kind, ch.Key, ch.What, ch.Fields))
		}
		for _, k := range p.files {
			diffs = append(diffs, "file "+k)
		}
		bc, nc := tagColour(b.settings, f.ColourTag), tagColour(now.settings, f.ColourTag)
		if bc != nc {
			diffs = append(diffs, fmt.Sprintf("tag colour %s → %s", bc, nc))
		}
		if want == "backup" {
			s.record(sc, f.Op, when+": server = backup", len(diffs) == 0, "%v", diffs)
		} else if f.Op != "new-item" {
			s.record(sc, f.Op, when+": server has the change", len(diffs) > 0, "no difference to the backup")
		}
		if f.New != "" {
			n := now.items[f.New]
			s.record(sc, f.Op, when+": item created after the backup is kept", n != nil && !trashed(n), "")
		}
		s.checkDesktop(sc, when, f, now)
	}
}

// checkDesktop compares the desktop's objects (fields, trash, file md5, tag
// colour) with the server's.
func (s *stress) checkDesktop(sc, when string, f family, now *libraryNow) {
	st, err := s.z.state(f)
	if err != nil {
		s.record(sc, f.Op, when+": desktop state", false, "%v", err)
		return
	}
	var diffs []string
	for _, k := range f.itemKeys() {
		l, sv := st[k], now.items[k]
		switch {
		case !l.Exists && s == nil:
		case l.Exists != (sv != nil):
			diffs = append(diffs, fmt.Sprintf("item %s: desktop has it %v, server %v", k, l.Exists, sv != nil))
		default:
			if d := differingFields(l.JSON, sv); len(d) > 0 {
				diffs = append(diffs, fmt.Sprintf("item %s fields %v", k, d))
			}
			if md5, _ := sv["md5"].(string); l.FileMD5 != "" && md5 != "" && l.FileMD5 != md5 {
				diffs = append(diffs, fmt.Sprintf("file %s: desktop md5 %s, server %s", k, l.FileMD5, md5))
			}
		}
	}
	for _, k := range []string{f.Coll, f.Sub} {
		l, sv := st[k], now.collections[k]
		if l.Exists != (sv != nil) {
			diffs = append(diffs, fmt.Sprintf("collection %s: desktop has it %v, server %v", k, l.Exists, sv != nil))
		} else if sv != nil {
			if d := differingFields(l.JSON, sv); len(d) > 0 {
				diffs = append(diffs, fmt.Sprintf("collection %s fields %v", k, d))
			}
		}
	}
	lc, _ := st["tagColour:"+f.ColourTag].JSON["color"].(string)
	if sc := tagColour(now.settings, f.ColourTag); !strings.EqualFold(lc, sc) {
		diffs = append(diffs, fmt.Sprintf("tag colour: desktop %q, server %q", lc, sc))
	}
	s.record(sc, f.Op, when+": desktop = server", len(diffs) == 0, "%v", diffs)
}

// extras: dry run, idempotence, item selection, read-only key, interrupted
// backup, prune.
func (s *stress) extras() error {
	const sc = "extras"
	list := []stressOp{stressOps[0]} // edit-title
	fams, err := s.families("extras", append(list, list[0]))
	if err != nil {
		return err
	}
	s.sync(sc, "sync fixtures", "fail")
	b0, err := s.backup()
	if err != nil {
		return err
	}
	for _, f := range fams {
		s.z.run(stressOps[0].desktop, map[string]string{"family": f.json()}, nil)
	}
	s.sync(sc, "sync changes", "fail")

	// dry run writes nothing
	_, h, _ := s.c.get(s.lib.Path + "/items?limit=1")
	before := h.Get("Last-Modified-Version")
	err = runRestore(fixedClient(s.c), b0.dir, s.profile, nil, true, false, io.Discard, quietProgress())
	_, h, _ = s.c.get(s.lib.Path + "/items?limit=1")
	s.record(sc, "dry-run", "library version unchanged", err == nil && before == h.Get("Last-Modified-Version"), "%v %s→%s", err, before, h.Get("Last-Modified-Version"))

	// selection: only the first family
	err = runRestore(fixedClient(s.c), b0.dir, s.profile, []string{fams[0].Parent}, false, true, io.Discard, quietProgress())
	now, _ := readLibrary(s.c, s.lib.Path)
	p0, p1 := makePlan(b0, now, nil, presentKeys(b0, fams[0])), makePlan(b0, now, nil, presentKeys(b0, fams[1]))
	s.record(sc, "--items", "selected family restored, the other not", err == nil && p0.empty() && !p1.empty(), "%v", err)

	// read-only key is refused
	if s.readOnly != nil {
		err = runRestore(fixedClient(s.readOnly), b0.dir, s.profile, nil, false, true, io.Discard, quietProgress())
		s.record(sc, "read-only key", "restore refused", err != nil && strings.Contains(err.Error(), "may not write"), "%v", err)
	}

	// full restore twice: the second finds nothing
	s.restore(sc, b0, "restore")
	var out strings.Builder
	err = runRestore(fixedClient(s.c), b0.dir, s.profile, nil, false, true, &out, quietProgress())
	s.record(sc, "restore twice", "second restore has nothing to do", err == nil && strings.Contains(out.String(), "The library matches the backup."), "%v", err)
	s.sync(sc, "sync", "fail")

	// interrupted backup (into its own folder, so files must be downloaded, not
	// linked): file downloads fail after 2; the next run resumes
	rootI := filepath.Join(s.root, "interrupted")
	failing := &Client{Base: s.c.Base, Key: s.c.Key, Attempts: 2, Sleep: func(time.Duration) {},
		HTTP: &http.Client{Transport: &failAfter{n: 2}, Timeout: time.Minute}}
	_, _, err = backupLibrary(failing, s.lib, rootI, nil, time.Now(), quietProgress())
	libI, _ := libraryFolder(rootI, s.lib)
	s.record(sc, "interrupted backup", "fails and leaves a .partial", err != nil && findPartial(libI) != "", "%v", err)
	time.Sleep(1100 * time.Millisecond)
	dir, _, err := backupLibrary(s.c, s.lib, rootI, nil, time.Now(), quietProgress())
	resumed := 0
	if err == nil {
		var files map[string]FileEntry
		readJSON(filepath.Join(dir, "files.json"), &files)
		for _, f := range files {
			if f.Source == "resumed" {
				resumed++
			}
		}
	}
	s.record(sc, "interrupted backup", "next run completes and reuses downloaded files", err == nil && findPartial(libI) == "" && resumed > 0, "%v, %d resumed", err, resumed)

	// prune on the test backups
	pr := s.profile
	pr.KeepDaily, pr.KeepWeekly, pr.KeepMonthly = 1, 0, 0
	var pout strings.Builder
	libRoot, _ := libraryFolder(s.root, s.lib)
	err = prune(s.root, pr, true, &pout)
	listed := strings.Count(pout.String(), "would delete "+filepath.Base(libRoot)+"/")
	s.record(sc, "prune", "dry run lists all but the newest", err == nil && listed == len(backups(libRoot))-1, "%v, %d listed", err, listed)
	return nil
}

// finish erases the fixtures, syncs, and checks that nothing outside the
// fixtures changed compared with the safety backup.
func (s *stress) finish(safetyDir string) {
	if s.keep {
		s.pr.Logf("fixtures kept (--keep)")
		return
	}
	var r map[string]int
	err := s.z.run(jsCleanup, map[string]string{"run": s.run}, &r)
	s.record("cleanup", "*", "erase fixtures in the desktop", err == nil, "%v %v", err, r)
	s.sync("cleanup", "sync", "remote")
	safety, err := loadBackup(safetyDir)
	if err == nil {
		now, err2 := readLibrary(s.c, s.lib.Path)
		if err = err2; err == nil {
			p := makePlan(safety, now, nil, nil)
			s.record("cleanup", "*", "library = safety backup (nothing else changed, no fixture left)",
				p.empty() && len(p.extra) == 0, "%d differing, %d files, settings %v, %d extra", len(p.objects), len(p.files), p.settings, len(p.extra))
		}
	}
	if err != nil {
		s.record("cleanup", "*", "compare with the safety backup", false, "%v", err)
	}
}

// writableLibraries lists the libraries a key may write to: the personal
// library and each group of its user with write access.
func writableLibraries(c *Client) ([]library, error) {
	k, err := c.keyInfo()
	if err != nil {
		return nil, err
	}
	var out []library
	if k.Access.User.Write {
		out = append(out, library{fmt.Sprintf("/users/%d", k.UserID), fmt.Sprintf("user-%d", k.UserID), k.Username})
	}
	groups, err := c.groups(k.UserID)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if k.GroupAccess(g.ID).Write {
			out = append(out, library{fmt.Sprintf("/groups/%d", g.ID), fmt.Sprintf("group-%d", g.ID), g.Data.Name})
		}
	}
	return out, nil
}

// checkKeyScope refuses a key that may write to any library not in named, so
// that even a fault in the test cannot change anything else on the server.
func checkKeyScope(c *Client, named map[string]bool) error {
	libs, err := writableLibraries(c)
	if err != nil {
		return err
	}
	var extra []string
	for _, l := range libs {
		if !named[l.ID] {
			extra = append(extra, l.Label())
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return fmt.Errorf("the write key may also write to %s, which this run does not test. "+
		"Use a key with write access only to the test libraries (zotero.org/settings/keys: per-group permissions, "+
		"personal library read-only unless it is tested); best, a separate Zotero account just for testing (docs/stresstest.md)",
		strings.Join(extra, ", "))
}
