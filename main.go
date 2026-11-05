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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const usage = `usage: zotero-backup <command> [options]

commands:
  backup      complete, dated backup of the profile's libraries; unchanged files
              are hard links to the previous backup
  list        list the backups in the profile's backup folder
  diff BACKUP     compare a backup (a backup folder, or a library folder for its
                  newest backup) with the library today
  restore BACKUP  make the library match the backup again, keeping all keys;
                  shows the plan and asks first (--dry-run: only show it;
                  --yes: do not ask; --items K1,K2: only these items, their
                  children and what they need)
  prune       delete backups not kept by keep_daily / keep_weekly / keep_monthly
              (newest per day, ISO week and month); --dry-run only shows them
  key check   show the user, libraries and permissions of the API key
  config      print the effective configuration (all options with their values)

options:
  --config PATH       configuration file (default: %s)
  --profile NAME      profile to use (default: default)
  --dry-run           prune, restore: show what would be done, do nothing
  --yes               restore: do not ask for confirmation
  --items K1,K2       diff, restore: only these items (with children and parents)
  --progress MODE     auto (default), line, log or quiet: how progress is shown
                      on stderr (profile option "progress"; "progress_interval"
                      sets the seconds between lines in log mode)
  --quiet             the same as --progress quiet
  --exclude A,B       backup: leave out these parts instead of the profile's
                      exclude list ("" for none); one of files, attachments,
                      notes, annotations, fulltext, settings, trash

The API key is read from ZOTERO_API_KEY, or else from the keychain entry named
by the profile's "key" (macOS only for now).
`

func main() {
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
	yes := fs.Bool("yes", false, "restore: do not ask")
	itemsFlag := fs.String("items", "", "diff / restore: only these item keys")
	progressFlag := fs.String("progress", "", "auto, line, log or quiet")
	quiet := fs.Bool("quiet", false, "only the summary and errors")
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, defaultConfigPath()) }

	if len(args) == 0 {
		fs.Usage()
		return errors.New("no command given")
	}
	cmd, rest := args[0], args[1:]
	if cmd == "key" && len(rest) > 0 {
		cmd, rest = "key "+rest[0], rest[1:]
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
		}
	})
	if err := profile.validate(); err != nil {
		return err
	}

	switch cmd {
	case "config":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg)
	case "key check":
		key, err := apiKey(profile)
		if err != nil {
			return err
		}
		return keyCheck(newClient(key), profile, out)
	case "backup":
		key, err := apiKey(profile)
		if err != nil {
			return err
		}
		return runBackup(newClient(key), profile, out, newProgress(profile.Progress, profile.ProgressInterval, os.Stderr))
	case "list":
		return listBackups(expandHome(profile.To), out)
	case "diff", "restore":
		if len(pos) != 1 {
			return fmt.Errorf("%s needs one backup folder", cmd)
		}
		key, err := apiKey(profile)
		if err != nil {
			return err
		}
		var sel []string
		if *itemsFlag != "" {
			sel = splitList(*itemsFlag)
		}
		if cmd == "diff" {
			return runDiff(newClient(key), expandHome(pos[0]), profile, sel, out)
		}
		return runRestore(newClient(key), expandHome(pos[0]), profile, sel, *dryRun, *yes, out,
			newProgress(profile.Progress, profile.ProgressInterval, os.Stderr))
	case "prune":
		return prune(expandHome(profile.To), profile, *dryRun, out)
	case "help", "-h", "--help":
		fs.Usage()
		return nil
	}
	fs.Usage()
	return fmt.Errorf("unknown command %q", strings.Join(args[:min(len(args), 2)], " "))
}

// apiKey returns ZOTERO_API_KEY, or the keychain entry named by the profile.
func apiKey(p Profile) (string, error) {
	if k := os.Getenv("ZOTERO_API_KEY"); k != "" {
		return k, nil
	}
	// ponytail: macOS keychain via the security tool; Linux / Windows keychains when needed
	if runtime.GOOS != "darwin" {
		return "", errors.New("set ZOTERO_API_KEY (keychain support is macOS-only for now)")
	}
	b, err := exec.Command("security", "find-generic-password", "-s", p.Key, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no API key: ZOTERO_API_KEY is not set and keychain entry %q was not found", p.Key)
	}
	return strings.TrimSpace(string(b)), nil
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
func runBackup(c *Client, p Profile, out io.Writer, pr *progress) error {
	libs, skipped, err := resolveLibraries(c, p.Library)
	if err != nil {
		return err
	}
	for _, s := range skipped {
		pr.Warnf("skipped %s", s)
	}
	root := expandHome(p.To)
	failed := 0
	for i, lib := range libs {
		pr.SetPrefix(fmt.Sprintf("%d/%d %s", i+1, len(libs), lib.Dir()))
		dir, m, err := backupLibrary(c, lib, root, p.Exclude, time.Now(), pr)
		if err != nil {
			failed++
			pr.Warnf("FAILED: %v", err)
			fmt.Fprintf(out, "%s: FAILED: %v\n", lib.ID, err)
			continue
		}
		fmt.Fprintf(out, "%s: %s  version %d, %d items, %d files (%d downloaded, %.1f MB; %d linked), %d missing, %d problems, %.0f s\n",
			lib.ID, dir, m.Version, itemCount(m.Counts), m.Counts["files"], m.Downloaded, float64(m.DownBytes)/1e6,
			m.Linked, len(m.Missing), len(m.Problems), m.Seconds)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d libraries failed", failed, len(libs))
	}
	return nil
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
