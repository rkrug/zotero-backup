package main

// schedule sets up the daily backup with the system's own scheduler (#36):
// a LaunchAgent on macOS, a systemd user timer on Linux, a scheduled task on
// Windows. The job runs "backup" and then "prune" with the chosen profile,
// while the user is logged in. Files it writes carry scheduleMark; it never
// overwrites or removes a file without it (e.g. one set up by hand).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const scheduleMark = "written by zotero-backup schedule"

type scheduler struct {
	goos, home, uid string
	exe             string   // absolute path of this program
	args            []string // --config / --profile for the job
	name            string   // org.zotero-backup[.profile] / zotero-backup[-profile]
	logDir          string   // the profile's run logs ("" = none: progress goes to the scheduler's log)
	run             func(name string, args ...string) (string, error)
	out             io.Writer
}

func newScheduler(goos, home, exe, profile, configPath string, out io.Writer) *scheduler {
	s := &scheduler{goos: goos, home: home, uid: strconv.Itoa(os.Getuid()), exe: exe, out: out, run: runTool}
	suffix := ""
	if profile != "default" {
		s.args = append(s.args, "--profile", profile)
		suffix = profile
	}
	if configPath != "" {
		s.args = append(s.args, "--config", configPath)
	}
	switch goos {
	case "darwin":
		s.name = "org.zotero-backup"
		if suffix != "" {
			s.name += "." + suffix
		}
	default:
		s.name = "zotero-backup"
		if suffix != "" {
			s.name += "-" + suffix
		}
	}
	return s
}

func runTool(name string, args ...string) (string, error) {
	b, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}

// files are the files the job consists of, with their content.
func (s *scheduler) files(hour, minute int) map[string]string {
	progress := []string{"--progress", "log"}
	if s.logDir != "" { // the run logs have everything; the scheduler's log keeps the summary, warnings and errors
		progress = []string{"--quiet"}
	}
	backup := append(append([]string{s.exe, "backup"}, s.args...), progress...)
	prune := append(append([]string{s.exe, "prune"}, s.args...), progress...)
	switch s.goos {
	case "darwin":
		log := filepath.Join(s.home, "Library", "Logs", "zotero-backup.log")
		cmd := shJoin(backup) + " && " + shJoin(prune)
		return map[string]string{s.plistPath(): fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- %s; remove with: zotero-backup schedule remove -->
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string><string>%s</string></array>
  <key>StartCalendarInterval</key><dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, scheduleMark, xmlEscape(s.name), xmlEscape(cmd), hour, minute, xmlEscape(log), xmlEscape(log))}
	case "windows":
		dir := filepath.Join(s.home, "AppData", "Local", "zotero-backup")
		log := filepath.Join(dir, "zotero-backup.log")
		return map[string]string{filepath.Join(dir, s.name+".cmd"): fmt.Sprintf("@echo off\r\nrem %s; remove with: zotero-backup schedule remove\r\nrem daily at %02d:%02d\r\n%s >> \"%s\" 2>&1\r\nif errorlevel 1 exit /b 1\r\n%s >> \"%s\" 2>&1\r\n",
			scheduleMark, hour, minute, cmdJoin(backup), log, cmdJoin(prune), log)}
	}
	dir := filepath.Join(s.home, ".config", "systemd", "user")
	return map[string]string{
		filepath.Join(dir, s.name+".service"): fmt.Sprintf("# %s; remove with: zotero-backup schedule remove\n[Unit]\nDescription=zotero-backup: back up Zotero libraries, then prune\n\n[Service]\nType=oneshot\nExecStart=%s\nExecStartPost=%s\n",
			scheduleMark, systemdJoin(backup), systemdJoin(prune)),
		filepath.Join(dir, s.name+".timer"): fmt.Sprintf("# %s; remove with: zotero-backup schedule remove\n[Unit]\nDescription=zotero-backup daily\n\n[Timer]\nOnCalendar=*-*-* %02d:%02d:00\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n",
			scheduleMark, hour, minute),
	}
}

func (s *scheduler) plistPath() string {
	return filepath.Join(s.home, "Library", "LaunchAgents", s.name+".plist")
}

// check refuses files that exist without the mark (set up by hand).
func (s *scheduler) check(paths []string) error {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil && !strings.Contains(string(b), scheduleMark) {
			return fmt.Errorf("%s exists and was not written by zotero-backup schedule (set up by hand?): remove or rename it first", p)
		}
	}
	return nil
}

func (s *scheduler) install(at string) error {
	m := regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`).FindStringSubmatch(at)
	if m == nil {
		return fmt.Errorf("--at %q: use HH:MM, e.g. 02:30", at)
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	if strings.Contains(s.exe, "go-build") || strings.HasPrefix(s.exe, os.TempDir()) {
		return fmt.Errorf("this program runs from a temporary folder (%s): install it first and run schedule from there", s.exe)
	}
	if onPath, _ := exec.LookPath("zotero-backup"); onPath != "" {
		if p, _ := filepath.EvalSymlinks(onPath); p != "" && p != s.exe {
			fmt.Fprintf(s.out, "note     the job runs %s, not the zotero-backup on your PATH (%s); to use that one, run schedule install with it\n", s.exe, p)
		}
	}
	files := s.files(hour, minute)
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	if err := s.check(paths); err != nil {
		return err
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "wrote    %s\n", p)
	}
	switch s.goos {
	case "darwin":
		s.run("launchctl", "bootout", "gui/"+s.uid+"/"+s.name) // an earlier version of it, if loaded
		if _, err := s.run("launchctl", "bootstrap", "gui/"+s.uid, s.plistPath()); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "loaded   %s: daily at %02d:%02d while you are logged in; log %s\n", s.name, hour, minute, filepath.Join(s.home, "Library", "Logs", "zotero-backup.log"))
	case "windows":
		script := paths[0]
		if _, err := s.run("schtasks", "/Create", "/F", "/SC", "DAILY", "/ST", fmt.Sprintf("%02d:%02d", hour, minute), "/TN", s.name, "/TR", `"`+script+`"`); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "created  task %s: daily at %02d:%02d while you are logged in; log next to %s\n", s.name, hour, minute, script)
	default:
		if _, err := s.run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if _, err := s.run("systemctl", "--user", "enable", "--now", s.name+".timer"); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "enabled  %s.timer: daily at %02d:%02d; log: journalctl --user -u %s\n", s.name, hour, minute, s.name)
	}
	fmt.Fprintln(s.out, "The first run needs the key in the keychain (zotero-backup key set). For runs while nobody is logged in see the documentation: Scheduled backups.")
	return nil
}

func (s *scheduler) installed() []string {
	var out []string
	for p := range s.files(0, 0) {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// show prints what is installed in short: when, what, log, state; raw
// prints the files and the scheduler's own output instead.
func (s *scheduler) show(raw bool) error {
	paths := s.installed()
	if len(paths) == 0 {
		fmt.Fprintf(s.out, "No schedule %s installed by zotero-backup schedule.\n", s.name)
		return nil
	}
	sort.Strings(paths)
	var state string
	var err error
	switch s.goos {
	case "darwin":
		state, err = s.run("launchctl", "print", "gui/"+s.uid+"/"+s.name)
	case "windows":
		state, err = s.run("schtasks", "/Query", "/TN", s.name, "/V", "/FO", "LIST")
	default:
		state, err = s.run("systemctl", "--user", "list-timers", s.name+".timer", "--all")
	}
	if raw {
		for _, p := range paths {
			b, _ := os.ReadFile(p)
			fmt.Fprintf(s.out, "== %s\n%s\n", p, b)
		}
		if err != nil {
			fmt.Fprintf(s.out, "not loaded: %v\n", err)
		} else {
			fmt.Fprint(s.out, state)
		}
		return nil
	}
	var all strings.Builder
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		all.Write(b)
	}
	at, cmds, log := parseSchedule(s.goos, all.String())
	kind := map[string]string{"darwin": "LaunchAgent", "windows": "scheduled task", "linux": "systemd user timer"}[s.goos]
	fmt.Fprintf(s.out, "schedule  %s (%s, runs while you are logged in)\n", s.name, kind)
	fmt.Fprintf(s.out, "when      daily at %s\n", at)
	if len(cmds) > 0 {
		profile, config := "default", "the default configuration file"
		for i, a := range cmds[0] {
			if i+1 < len(cmds[0]) && a == "--profile" {
				profile = cmds[0][i+1]
			}
			if i+1 < len(cmds[0]) && a == "--config" {
				config = cmds[0][i+1]
			}
		}
		fmt.Fprintf(s.out, "profile   %s (in %s; zotero-backup config shows it)\n", profile, config)
	}
	for i, c := range cmds {
		label := "runs"
		if i > 0 {
			label = "then"
		}
		fmt.Fprintf(s.out, "%-9s %s\n", label, strings.Join(c, " "))
	}
	if len(cmds) > 0 {
		if onPath, _ := exec.LookPath("zotero-backup"); onPath != "" {
			if p, _ := filepath.EvalSymlinks(onPath); p != "" && p != cmds[0][0] {
				fmt.Fprintf(s.out, "          (not the zotero-backup on your PATH, %s: run schedule install from that one to use it)\n", p)
			}
		}
	}
	if s.logDir != "" {
		fmt.Fprintf(s.out, "run logs  %s (one per run; prune archives the old ones)\n", s.logDir)
	}
	if log != "" {
		fmt.Fprintf(s.out, "log       %s\n", log)
	} else if s.goos == "linux" {
		fmt.Fprintf(s.out, "log       journalctl --user -u %s\n", s.name)
	}
	for _, p := range paths {
		fmt.Fprintf(s.out, "file      %s\n", p)
	}
	if err != nil {
		fmt.Fprintln(s.out, "state     not loaded (zotero-backup schedule show raw for details)")
		return nil
	}
	fmt.Fprintf(s.out, "state     %s\n", stateSummary(s.goos, state))
	return nil
}

// parseSchedule reads back the time, the commands and the log from the files
// schedule wrote.
func parseSchedule(goos, content string) (at string, cmds [][]string, log string) {
	find := func(re string) string {
		if m := regexp.MustCompile(re).FindStringSubmatch(content); m != nil {
			return m[1]
		}
		return ""
	}
	switch goos {
	case "darwin":
		h, _ := strconv.Atoi(find(`<key>Hour</key><integer>(\d+)</integer>`))
		m, _ := strconv.Atoi(find(`<key>Minute</key><integer>(\d+)</integer>`))
		at = fmt.Sprintf("%02d:%02d", h, m)
		sh := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(find(`<string>-c</string><string>(.*?)</string>`))
		cmds = quotedCommands(sh, '\'')
		log = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(find(`<key>StandardOutPath</key><string>(.*?)</string>`))
	case "windows":
		at = find(`rem daily at (\d\d:\d\d)`)
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, `"`) {
				cmd, rest, _ := strings.Cut(line, " >> ")
				cmds = append(cmds, quotedCommands(strings.ReplaceAll(cmd, "%%", "%"), '"')...)
				log = strings.Trim(strings.Fields(rest + " x")[0], `"`)
			}
		}
	default:
		at = find(`OnCalendar=\*-\*-\* (\d\d:\d\d)`)
		for _, k := range []string{"ExecStart", "ExecStartPost"} {
			line := find(`(?m)^` + k + `=(.*)$`)
			line = strings.NewReplacer(`\\`, `\`, `\"`, "\x00", "%%", "%", "$$", "$").Replace(line)
			for _, c := range quotedCommands(line, '"') {
				for i := range c {
					c[i] = strings.ReplaceAll(c[i], "\x00", `"`)
				}
				cmds = append(cmds, c)
			}
		}
	}
	if at == "" {
		at = "?"
	}
	return at, cmds, log
}

// quotedCommands splits text made of quoted words (as shJoin / systemdJoin /
// cmdJoin write them) into commands, separated by "&&". For sh, '\” is a
// quote inside a word.
func quotedCommands(text string, q rune) [][]string {
	var cmds [][]string
	var cur []string
	var word strings.Builder
	in, started := false, false
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == q && q == '\'' && in && i+3 < len(rs) && string(rs[i:i+4]) == `'\''`:
			word.WriteRune('\'')
			i += 3
		case r == q:
			in = !in
			started = true
		case in:
			word.WriteRune(r)
		case r == ' ':
			if started {
				cur = append(cur, word.String())
				word.Reset()
				started = false
			}
		case r == '&' && i+1 < len(rs) && rs[i+1] == '&':
			if len(cur) > 0 {
				cmds = append(cmds, cur)
			}
			cur = nil
			i++
		}
	}
	if started {
		cur = append(cur, word.String())
	}
	if len(cur) > 0 {
		cmds = append(cmds, cur)
	}
	return cmds
}

// stateSummary picks the useful lines from the scheduler's output.
func stateSummary(goos, out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch goos {
		case "darwin":
			if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
				continue // only the job's own lines, not those of its sub-entries
			}
			for _, k := range []string{"state = ", "runs = ", "last exit code = "} {
				if strings.HasPrefix(t, k) && !slices.Contains(keep, t) {
					keep = append(keep, t)
				}
			}
		case "windows":
			for _, k := range []string{"Status:", "Next Run Time:", "Last Run Time:", "Last Result:"} {
				if strings.HasPrefix(t, k) {
					keep = append(keep, strings.Join(strings.Fields(t), " "))
				}
			}
		default:
			if strings.Contains(t, ".timer") {
				keep = append(keep, strings.Join(strings.Fields(t), " "))
			}
		}
	}
	if len(keep) == 0 {
		return "loaded (zotero-backup schedule show raw for details)"
	}
	return strings.Join(keep, "; ")
}

func (s *scheduler) remove() error {
	paths := s.installed()
	if len(paths) == 0 {
		return fmt.Errorf("no schedule %s installed by zotero-backup schedule", s.name)
	}
	if err := s.check(paths); err != nil {
		return err
	}
	switch s.goos {
	case "darwin":
		s.run("launchctl", "bootout", "gui/"+s.uid+"/"+s.name)
	case "windows":
		s.run("schtasks", "/Delete", "/TN", s.name, "/F")
	default:
		s.run("systemctl", "--user", "disable", "--now", s.name+".timer")
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "removed  %s\n", p)
	}
	if s.goos == "linux" {
		s.run("systemctl", "--user", "daemon-reload")
	}
	return nil
}

// runSchedule is the schedule command: install, show or remove.
func runSchedule(action, at, profile, configPath, backups, logDir string, raw bool, out io.Writer) error {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	goos := runtime.GOOS
	if goos != "darwin" && goos != "windows" && goos != "linux" {
		return fmt.Errorf("schedule: not on %s; see the documentation: Scheduled backups", goos)
	}
	s := newScheduler(goos, home, exe, profile, configPath, out)
	s.logDir = logDir
	switch action {
	case "install":
		return s.install(at)
	case "show":
		err := s.show(raw)
		if err == nil && !raw {
			printLastRun(backups, out) // the last backup run with this profile's folder, scheduled or not
		}
		return err
	case "remove":
		return s.remove()
	}
	return errors.New("usage: zotero-backup schedule install [--at HH:MM] | show [raw] | remove")
}

// shJoin quotes arguments for /bin/sh.
func shJoin(args []string) string {
	var q []string
	for _, a := range args {
		q = append(q, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	return strings.Join(q, " ")
}

// systemdJoin quotes arguments for ExecStart (double quotes; %, \ and " escaped).
func systemdJoin(args []string) string {
	var q []string
	for _, a := range args {
		a = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(a)
		q = append(q, `"`+a+`"`)
	}
	return strings.Join(q, " ")
}

// cmdJoin quotes arguments for a .cmd script (paths with spaces; % doubled).
func cmdJoin(args []string) string {
	var q []string
	for _, a := range args {
		q = append(q, `"`+strings.ReplaceAll(a, "%", "%%")+`"`)
	}
	return strings.Join(q, " ")
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
