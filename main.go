// Command zotero-backup backs up and restores Zotero libraries through the
// Zotero web API, keeping the item keys. See Issue #1 for the plan.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `usage: zotero-backup <command> [options]

commands:
  backup      complete, dated backup of the profile's libraries; unchanged files
              are hard links to the previous backup
  list        list the backups in the profile's backup folder
  diff BACKUP     compare a backup (a backup folder, or a library folder for its
                  newest backup) with the library today
  diff BACKUP1 BACKUP2  compare two backups of a library on disk (offline):
                  what was added, removed or changed between them
  restore BACKUP  make the library match the backup again, keeping all keys;
                  shows the plan and asks first (--dry-run: only show it;
                  --yes: do not ask; --items K1,K2: only these items, their
                  children and what they need). Objects not in the backup are
                  kept, unless --trash-new (moved to the trash) or --erase-new
                  (erased for good, the trash too)
  find TEXT [LIBRARY_FOLDER]  search the backups (offline) for items whose
              title, creators, DOI, key, note or file name contains TEXT: in
              which backups each is, and whether it is gone from the newest
  show BACKUP KEY  print an item from a backup with its notes, attachments and
              annotations (offline); --save DIR copies its files out
  schedule install [--at HH:MM] | show [raw] | remove  a daily backup, then prune,
              with the system's scheduler (macOS LaunchAgent, systemd user
              timer, Windows task) while you are logged in; --profile and
              --config are passed on
  notify-test  send a test notification with the profile's notify_command /
              notify_macos (sent after a backup that failed or had problems)
  verify [BACKUP | LIBRARY_FOLDER | all]  check backups on disk (offline): the
              JSON files, the counts, every file's size and md5, attachments
              backed up or listed as missing; without an argument the newest
              backup of each library; --quick: sizes only
  status      the backups running now (from their locks): library, PID, since
  stop [LIBRARY]  stop the backups running on this computer (all, or the one of
              user / group:<id>) cleanly; an unfinished backup is continued by
              the next run
  prune       delete backups not kept by keep_daily / keep_weekly / keep_monthly
              (newest per day, ISO week and month); --dry-run only shows them
  key set [NAME]     store an API key in the system keychain under NAME (default:
                     the profile's "key"); asks for it without echo, checks it
  key check [NAME]   show the user, libraries and permissions of the API key
  key remove [NAME]  delete the keychain entry
  config      print the effective configuration (all options with their values)
  version     print the version
  upgrade [stable|beta]  replace this program with the newest release from
              GitHub (signed and checked); without a channel it shows what
              there is and asks (--check: only show; --yes: do not ask;
              stable --downgrade: from a pre-release back to stable)

options:
  --config PATH       configuration file (default: %s)
  --profile NAME      profile to use (default: default)
  --dry-run           prune, restore: show what would be done, do nothing
  --yes               restore, upgrade: do not ask for confirmation
  --trash-new         restore: also move objects not in the backup to the trash
  --erase-new         restore: also erase objects not in the backup for good,
                      also those in the trash (command line only, never from
                      the configuration; not with --items)
  --quick             verify: check sizes only, not the md5 of every file
  --save DIR          show: copy the item's files (ZIPs unpacked) into DIR/<key>/
  --check             upgrade: only show what is available (exit status 1 if
                      an update is available)
  --downgrade         upgrade stable: go back from a pre-release
  --items K1,K2       diff, restore: only these items (with children and parents)
  --progress MODE     auto (default), line, log or quiet: how progress is shown
                      on stderr (profile option "progress"; "progress_interval"
                      sets the seconds between lines in log mode)
  --quiet             the same as --progress quiet
  --key NAME          use this keychain entry for every library in this run,
                      instead of the profile's "key" / "keys" (e.g. a write key
                      for a restore); without it, restore uses the profile's
                      "write_key" if set
  --exclude A,B       leave out these parts instead of the profile's exclude
                      list ("" for none): backup does not back them up, diff
                      and restore leave them out of the plan; one of files,
                      attachments, notes, annotations, fulltext, settings, trash

The API key is read from ZOTERO_API_KEY, or else from the keychain entry named
by the profile's "key": macOS Keychain, Windows Credential Manager, Linux Secret
Service (secret-tool).
`

func main() {
	removeOldExe()
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "zotero-backup:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("zotero-backup", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "configuration file")
	profileName := fs.String("profile", "default", "profile to use")
	excludeFlag := fs.String("exclude", "", "parts to leave out")
	dryRun := fs.Bool("dry-run", false, "prune / restore: only show what would be done")
	yes := fs.Bool("yes", false, "restore, upgrade: do not ask")
	trashNew := fs.Bool("trash-new", false, "restore: move objects not in the backup to the trash")
	eraseNew := fs.Bool("erase-new", false, "restore: erase objects not in the backup")
	scheduleAt := fs.String("at", "06:00", "schedule install: the daily time, HH:MM")
	quickVerify := fs.Bool("quick", false, "verify: sizes only, no md5")
	saveDir := fs.String("save", "", "show: copy the item's files into this folder")
	check := fs.Bool("check", false, "upgrade: only show what is available")
	downgrade := fs.Bool("downgrade", false, "upgrade stable: back from a pre-release")
	itemsFlag := fs.String("items", "", "diff / restore: only these item keys")
	progressFlag := fs.String("progress", "", "auto, line, log or quiet")
	quiet := fs.Bool("quiet", false, "only the summary and errors")
	keyFlag := fs.String("key", "", "keychain entry to use for every library in this run")
	// stresstest (not in the help text; see docs/stresstest.md)
	var stressLibs listFlag
	fs.Var(&stressLibs, "library", "stresstest: a test library (repeat)")
	stressScen := fs.String("scenarios", "", "stresstest: scenarios")
	stressOpsFlag := fs.String("ops", "", "stresstest: operations")
	stressReportDir := fs.String("report", "", "stresstest: report folder")
	stressKeep := fs.Bool("keep", false, "stresstest: keep the test objects")
	zoteroProfile := fs.String("zotero-profile", "", "stresstest: Zotero profile")
	stressCleanupRun := fs.String("cleanup", "", "stresstest: only erase the test objects of this run")
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, defaultConfigPath()) }

	if len(args) == 0 {
		fs.Usage()
		return errors.New("no command given")
	}
	cmd, rest := args[0], args[1:]
	if cmd == "version" || cmd == "--version" {
		fmt.Fprintln(out, "zotero-backup", version)
		return nil
	}
	if cmd == "key" && len(rest) > 0 {
		cmd, rest = "key "+rest[0], rest[1:]
	}
	if cmd == "stresstest" {
		fs.Usage = func() { fmt.Fprint(fs.Output(), stressUsage()) }
		if len(rest) == 0 { // the steps to run it; any option starts it
			fmt.Fprint(out, stressUsage())
			return nil
		}
	}
	var pos []string // positional arguments, flags may come before or after them
	for {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos, rest = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if cmd == "upgrade" { // needs no configuration
		if len(pos) > 1 || (len(pos) == 1 && pos[0] != "stable" && pos[0] != "beta") {
			return errors.New("usage: zotero-backup upgrade [stable|beta] [--check] [--yes] [--downgrade]")
		}
		u, err := newUpdater(out)
		if err != nil {
			return err
		}
		return u.run(strings.Join(pos, ""), *check, *yes, *downgrade)
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[*profileName]
	if !ok {
		return fmt.Errorf("no profile %q in %s", *profileName, *cfgPath)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "exclude":
			profile.Exclude = splitList(*excludeFlag)
		case "progress":
			profile.Progress = *progressFlag
		case "quiet":
			if *quiet {
				profile.Progress = "quiet"
			}
		case "key":
			profile.Key, profile.Keys, profile.WriteKey = *keyFlag, map[string]string{}, ""
		}
	})
	if err := profile.validate(); err != nil {
		return err
	}

	switch cmd {
	case "config":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(cfg)
	case "key check", "key set", "key remove":
		if len(pos) > 1 {
			return fmt.Errorf("%s takes at most one key name", cmd)
		}
		if len(pos) == 1 {
			profile.Key, profile.Keys = pos[0], map[string]string{}
		}
		switch cmd {
		case "key set":
			return keySet(profile.Key, newClient, os.Stdin, out)
		case "key remove":
			return keyRemove(profile.Key, out)
		}
		return keyCheckAll(profile, newClient, out)
	case "backup":
		stopOnSignal(os.Stderr)
		return backupAndNotify(clientsFor(profile, newClient), profile, out, os.Stderr, newProgress(profile.Progress, profile.ProgressInterval, os.Stderr))
	case "schedule":
		raw := len(pos) == 2 && pos[0] == "show" && pos[1] == "raw"
		if len(pos) != 1 && !raw {
			return errors.New("usage: zotero-backup schedule install [--at HH:MM] | show [raw] | remove")
		}
		cfgArg := ""
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "config" {
				cfgArg, _ = filepath.Abs(expandHome(*cfgPath))
			}
		})
		return runSchedule(pos[0], *scheduleAt, *profileName, cfgArg, raw, out)
	case "notify-test":
		return notifyTest(profile, out)
	case "list":
		return listBackups(expandHome(profile.To), out)
	case "diff", "restore":
		if len(pos) != 1 && !(cmd == "diff" && len(pos) == 2) {
			return fmt.Errorf("%s needs one backup folder (diff: or two, to compare them)", cmd)
		}
		var sel []string
		if *itemsFlag != "" {
			sel = splitList(*itemsFlag)
		}
		remove := ""
		switch {
		case *trashNew && *eraseNew:
			return errors.New("--trash-new or --erase-new, not both")
		case (*trashNew || *eraseNew) && cmd != "restore":
			return errors.New("--trash-new / --erase-new are for restore")
		case *trashNew:
			remove = "trash"
		case *eraseNew:
			remove = "erase"
		}
		if cmd == "diff" && len(pos) == 2 {
			return runDiffBackups(expandHome(pos[0]), expandHome(pos[1]), profile, sel, out)
		}
		if cmd == "diff" {
			return runDiff(clientsFor(profile, newClient), expandHome(pos[0]), profile, sel, out)
		}
		return runRestore(clientsFor(profile.forRestore(), newClient), expandHome(pos[0]), profile, sel, remove, *dryRun, *yes, out,
			newProgress(profile.Progress, profile.ProgressInterval, os.Stderr))
	case "stresstest":
		return runStresstest(profile, stressOptions{libraries: stressLibs, scenarios: splitList(*stressScen),
			ops: splitList(*stressOpsFlag), report: *stressReportDir, keep: *stressKeep, zoteroProfile: *zoteroProfile, cleanup: *stressCleanupRun},
			os.Stdin, out, newProgress(profile.Progress, profile.ProgressInterval, os.Stderr))
	case "find":
		if len(pos) < 1 || len(pos) > 2 {
			return errors.New("usage: zotero-backup find TEXT [LIBRARY_FOLDER]")
		}
		lib := ""
		if len(pos) == 2 {
			lib = expandHome(pos[1])
		}
		return runFind(expandHome(profile.To), lib, pos[0], out)
	case "show":
		if len(pos) != 2 {
			return errors.New("usage: zotero-backup show BACKUP KEY [--save DIR]")
		}
		return runShow(expandHome(pos[0]), pos[1], expandHome(*saveDir), out)
	case "verify":
		if len(pos) > 1 {
			return errors.New("usage: zotero-backup verify [BACKUP | LIBRARY_FOLDER | all] [--quick]")
		}
		target := ""
		if len(pos) == 1 {
			target = pos[0]
			if target != "all" {
				target = expandHome(target)
			}
		}
		return runVerify(expandHome(profile.To), target, *quickVerify, out)
	case "status":
		return runStatus(expandHome(profile.To), out)
	case "stop":
		if len(pos) > 1 {
			return errors.New("usage: zotero-backup stop [LIBRARY], e.g. group:2333076 or user")
		}
		lib := ""
		if len(pos) == 1 {
			kind, id, err := parseLibrary(pos[0])
			if err != nil || kind == "all" {
				return fmt.Errorf("stop: %q is not user or group:<id>", pos[0])
			}
			lib = "user"
			if kind == "group" {
				lib = "group-" + strconv.Itoa(id)
			}
		}
		return runStop(expandHome(profile.To), lib, out)
	case "prune":
		stopOnSignal(os.Stderr)
		return prune(expandHome(profile.To), profile, *dryRun, out)
	case "help", "-h", "--help":
		fs.Usage()
		return nil
	}
	fs.Usage()
	return fmt.Errorf("unknown command %q", strings.Join(args[:min(len(args), 2)], " "))
}

// keyCheckAll checks every key the profile uses, saying which libraries each is for.
func keyCheckAll(p Profile, newClientFor func(string) *Client, out io.Writer) error {
	names := p.keyNames()
	var failed []string
	for _, name := range sortedKeys(names) {
		if len(names) > 1 {
			fmt.Fprintf(out, "== key %q, for: %s\n", name, strings.Join(names[name], ", "))
		}
		key, err := apiKeyNamed(name)
		if err == nil {
			err = keyCheck(newClientFor(key), p, out)
		}
		if err != nil {
			fmt.Fprintf(out, "%v\n", err)
			failed = append(failed, name)
		}
		if len(names) > 1 {
			fmt.Fprintln(out)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("key check failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// keyCheck prints who the key belongs to and what it may do in each library,
// marking the libraries the profile covers. It never prints the key.
func keyCheck(c *Client, p Profile, out io.Writer) error {
	k, err := c.keyInfo()
	if err != nil {
		return err
	}
	groups, err := c.groups(k.UserID)
	if err != nil {
		return err
	}
	kind, gid, _ := parseLibrary(p.Library)
	mark := func(isUser bool, id int) string {
		if kind == "all" || (kind == "user" && isUser) || (kind == "group" && !isUser && id == gid) {
			return "*"
		}
		return " "
	}
	fmt.Fprintf(out, "user %s (%d)\n\n", k.Username, k.UserID)
	fmt.Fprintf(out, "%s user          %s\n", mark(true, 0), rights(k.Access.User, true))
	found := kind != "group"
	for _, g := range groups {
		fmt.Fprintf(out, "%s group:%-7d %s  %s\n", mark(false, g.ID), g.ID, rights(k.GroupAccess(g.ID), false), g.Data.Name)
		found = found || g.ID == gid
	}
	fmt.Fprintf(out, "\n* = covered by profile library %q\n", p.Library)
	if !found {
		return fmt.Errorf("profile library %q: the key's user is not a member of this group", p.Library)
	}
	return nil
}

func rights(a Access, user bool) string {
	if !a.Library {
		return "no access"
	}
	s := "read"
	if a.Write {
		s = "read/write"
	}
	if user {
		if a.Notes {
			s += ", notes"
		}
		if a.Files {
			s += ", files"
		}
	}
	return s
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// runBackup backs up every library of the profile; a failing library does not
// stop the others, but makes the command fail at the end.
// runBackup backs up the profile's libraries; it returns the number of
// problems reported and the libraries that failed.
func runBackup(clientFor func(string) (*Client, error), p Profile, out io.Writer, pr *progress) (int, []string, error) {
	jobs, skipped, err := libraryJobs(p, clientFor)
	if err != nil {
		return 0, nil, err
	}
	for _, s := range skipped {
		pr.Warnf("skipped %s", s)
	}
	root := expandHome(p.To)
	failed, problems := 0, 0
	var failedLibs []string
	for i, j := range jobs {
		lib := j.lib
		pr.SetPrefix(fmt.Sprintf("%d/%d %s", i+1, len(jobs), lib.Dir()))
		dir, m, err := backupLibrary(j.c, lib, root, p.Exclude, time.Now(), pr)
		if err != nil {
			failed++
			failedLibs = append(failedLibs, lib.Label())
			pr.Warnf("FAILED: %v", err)
			fmt.Fprintf(out, "%s: FAILED: %v\n", lib.Label(), err)
			continue
		}
		fmt.Fprintf(out, "%s: %s  version %d, %d items, %d files (%d downloaded, %.1f MB; %d linked), %d missing, %d problems, %.0f s\n",
			lib.Label(), dir, m.Version, itemCount(m.Counts), m.Counts["files"], m.Downloaded, float64(m.DownBytes)/1e6,
			m.Linked, len(m.Missing), len(m.Problems), m.Seconds)
		problems += len(m.Problems)
	}
	if failed > 0 {
		return problems, failedLibs, fmt.Errorf("%d of %d libraries failed", failed, len(jobs))
	}
	return problems, nil, nil
}

// itemCount sums the item counts (the counts that are item types).
func itemCount(c map[string]int) int {
	n := 0
	for k, v := range c {
		if k != "collections" && k != "searches" && k != "fulltext" && k != "files" {
			n += v
		}
	}
	return n
}

// listBackups prints every backup below root, per library, oldest first.
func listBackups(root string, out io.Writer) error {
	libs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, l := range libs {
		if !l.IsDir() {
			continue
		}
		libRoot := filepath.Join(root, l.Name())
		names := backups(libRoot)
		if len(names) == 0 {
			continue
		}
		var last Meta
		readJSON(filepath.Join(libRoot, names[len(names)-1], "meta.json"), &last)
		fmt.Fprintln(out, l.Name())
		for _, n := range names {
			var m Meta
			if err := readJSON(filepath.Join(libRoot, n, "meta.json"), &m); err != nil {
				fmt.Fprintf(out, "  %s  (no meta.json)\n", n)
				continue
			}
			fmt.Fprintf(out, "  %s  version %-7d %6d items %6d files  %3d missing  %d problems\n",
				n, m.Version, itemCount(m.Counts), m.Counts["files"], len(m.Missing), len(m.Problems))
		}
		if p := findPartial(libRoot); p != "" {
			fmt.Fprintf(out, "  %s  (interrupted; resumed by the next backup)\n", filepath.Base(p))
		}
	}
	return nil
}

type libraryJob struct {
	lib library
	c   *Client
}

// libraryJobs resolves the profile's libraries, each with the client for its
// key. For "all", the default key lists the libraries; a library with a key of
// its own in "keys" is read with that key, also when the default key cannot.
func libraryJobs(p Profile, clientFor func(string) (*Client, error)) ([]libraryJob, []string, error) {
	kind, _, err := parseLibrary(p.Library)
	if err != nil {
		return nil, nil, err
	}
	if kind != "all" {
		c, err := clientFor(p.Library)
		if err != nil {
			return nil, nil, err
		}
		libs, skipped, err := resolveLibraries(c, p.Library)
		if err != nil {
			return nil, nil, err
		}
		return []libraryJob{{libs[0], c}}, skipped, nil
	}
	c, err := clientFor("default")
	if err != nil {
		return nil, nil, err
	}
	libs, skipped, err := resolveLibraries(c, "all")
	if err != nil {
		return nil, nil, err
	}
	var jobs []libraryJob
	done := map[string]bool{}
	for _, l := range libs {
		if _, own := p.Keys[l.Spec()]; own {
			continue // read with its own key below
		}
		jobs = append(jobs, libraryJob{l, c})
		done[l.Spec()] = true
	}
	for _, spec := range sortedKeys(p.Keys) {
		if spec == "default" || done[spec] {
			continue
		}
		c2, err := clientFor(spec)
		if err == nil {
			var ls []library
			if ls, _, err = resolveLibraries(c2, spec); err == nil {
				jobs = append(jobs, libraryJob{ls[0], c2})
				skipped = slices.DeleteFunc(skipped, func(s string) bool { return strings.HasPrefix(s, ls[0].ID+" ") || strings.HasPrefix(s, ls[0].ID+":") })
				continue
			}
		}
		skipped = append(skipped, fmt.Sprintf("%s (key %q): %v", spec, p.keyName(spec), err))
	}
	return jobs, skipped, nil
}

// listFlag collects a repeated option (--library A --library B).
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }
