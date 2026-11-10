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
	"strconv"
	"strings"
)

const scheduleMark = "written by zotero-backup schedule"

type scheduler struct {
	goos, home, uid string
	exe             string   // absolute path of this program
	args            []string // --config / --profile for the job
	name            string   // org.zotero-backup[.profile] / zotero-backup[-profile]
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
	backup := append(append([]string{s.exe, "backup"}, s.args...), "--progress", "log")
	prune := append(append([]string{s.exe, "prune"}, s.args...), "--progress", "log")
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
		return map[string]string{filepath.Join(dir, s.name+".cmd"): fmt.Sprintf("@echo off\r\nrem %s; remove with: zotero-backup schedule remove\r\n%s >> \"%s\" 2>&1\r\nif errorlevel 1 exit /b 1\r\n%s >> \"%s\" 2>&1\r\n",
			scheduleMark, cmdJoin(backup), log, cmdJoin(prune), log)}
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

func (s *scheduler) show() error {
	paths := s.installed()
	if len(paths) == 0 {
		fmt.Fprintf(s.out, "No schedule %s installed by zotero-backup schedule.\n", s.name)
		return nil
	}
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		fmt.Fprintf(s.out, "== %s\n%s\n", p, b)
	}
	var out string
	var err error
	switch s.goos {
	case "darwin":
		out, err = s.run("launchctl", "print", "gui/"+s.uid+"/"+s.name)
	case "windows":
		out, err = s.run("schtasks", "/Query", "/TN", s.name, "/V", "/FO", "LIST")
	default:
		out, err = s.run("systemctl", "--user", "list-timers", s.name+".timer", "--all")
	}
	if err != nil {
		fmt.Fprintf(s.out, "not loaded: %v\n", err)
		return nil
	}
	fmt.Fprint(s.out, out)
	return nil
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
func runSchedule(action, at, profile, configPath string, out io.Writer) error {
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
	switch action {
	case "install":
		return s.install(at)
	case "show":
		return s.show()
	case "remove":
		return s.remove()
	}
	return errors.New("usage: zotero-backup schedule install [--at HH:MM] | show | remove")
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
