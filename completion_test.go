package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// completionSetup makes a configuration with two profiles and a backup folder
// with a group and a user library.
func completionSetup(t *testing.T) (cfg, root string) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "backups")
	for _, b := range []string{"group-12-Our_Group/2026-11-01T020000Z", "group-12-Our_Group/2026-11-02T020000Z", "user-7-me/2026-11-02T020000Z", "notes/x"} {
		os.MkdirAll(filepath.Join(root, b), 0o755)
	}
	os.MkdirAll(filepath.Join(root, "group-13-Empty"), 0o755) // no backups: not offered
	cfg = filepath.Join(dir, "config.json")
	data := `{"profiles": {"default": {"to": "` + filepath.ToSlash(root) + `"}, "work": {"key": "zb-work", "write_key": "zb-write", "keys": {"group:12": "zb-group"}}}}`
	if err := os.WriteFile(cfg, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, root
}

func completeWords(t *testing.T, words ...string) []string {
	t.Helper()
	var out strings.Builder
	if err := run(append([]string{"__complete"}, words...), &out); err != nil {
		t.Fatal(err)
	}
	return strings.Fields(out.String())
}

func TestComplete(t *testing.T) {
	cfg, root := completionSetup(t)
	sep := string(filepath.Separator)
	g12, u7 := filepath.Join(root, "group-12-Our_Group"), filepath.Join(root, "user-7-me")
	cases := []struct {
		words []string
		want  string
	}{
		{[]string{"+re"}, "restore"},
		{[]string{"+st"}, "status stop"},             // not the hidden stresstest
		{[]string{"+--pro"}, "--profile --progress"}, // flags of every command
		{[]string{"upgrade", "+--"}, "--check --config --downgrade --exclude --key --profile --progress --quiet --yes"},
		{[]string{"verify", "+--q"}, "--quick --quiet"},
		{[]string{"key", "+"}, "check remove set"},
		{[]string{"schedule", "show", "+"}, "raw"},
		{[]string{"completion", "+"}, "bash fish powershell zsh"},
		{[]string{"--config", cfg, "--profile", "+"}, "default work"},
		{[]string{"backup", "--exclude", "+files,f"}, "files,fulltext"},
		{[]string{"--progress", "+l"}, "line log"},
		{[]string{"--config", cfg, "--profile", "work", "key", "set", "+"}, "zb-group zb-work zb-write"},
		{[]string{"--config=" + cfg, "--profile=work", "restore", "--key", "+zb-w"}, "zb-work zb-write"},
		{[]string{"--config", cfg, "restore", "+"}, g12 + sep + " " + u7 + sep},
		{[]string{"--config", cfg, "--dry-run", "restore", "+" + g12 + sep}, g12 + sep + " " + filepath.Join(g12, "2026-11-01T020000Z") + " " + filepath.Join(g12, "2026-11-02T020000Z")},
		{[]string{"--config", cfg, "diff", g12, "+" + u7}, u7 + sep},
		{[]string{"--config", cfg, "verify", "+a"}, "all"},
		{[]string{"--config", cfg, "find", "text", "+"}, g12 + sep + " " + u7 + sep},
		{[]string{"--config", cfg, "stop", "+"}, "group:12 user"},
		{[]string{"--config", cfg, "restore", "+./"}, ""}, // the shell completes file names
		{[]string{"--config", cfg, "show", g12, "+"}, ""},
		{[]string{"--config", filepath.Join(root, "missing.json"), "--profile", "+"}, "default"},
		{[]string{"--config", filepath.Join(root, "notes", "x"), "restore", "+"}, ""}, // unreadable: nothing, no error
	}
	for _, c := range cases {
		if got := strings.Join(completeWords(t, c.words...), " "); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.words, got, c.want)
		}
	}
}

func TestCompleteWithTilde(t *testing.T) {
	cfg, _ := completionSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	os.MkdirAll(filepath.Join(home, "zb", "user-7-me", "2026-11-02T020000Z"), 0o755)
	os.WriteFile(cfg, []byte(`{"profiles": {"default": {"to": "~/zb"}}}`), 0o644)
	got := completeWords(t, "--config", cfg, "restore", "+~/zb/u")
	if want := "~" + string(filepath.Separator) + filepath.Join("zb", "user-7-me") + string(filepath.Separator); len(got) != 1 || got[0] != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCompletionCoversTheCommands: every command in the help is completed.
func TestCompletionCoversTheCommands(t *testing.T) {
	in := false
	for _, l := range strings.Split(usage, "\n") {
		switch {
		case l == "commands:":
			in = true
		case l == "options:":
			in = false
		case in && strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   "):
			if cmd := strings.Fields(l)[0]; completeCommands[cmd] == nil && !slices.Contains(completeWords(t, "+"), cmd) {
				t.Errorf("command %q is not completed", cmd)
			}
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "fish", "powershell"} {
		var out strings.Builder
		if err := run([]string{"completion", sh}, &out); err != nil || !strings.Contains(out.String(), "__complete") {
			t.Errorf("%s: %v\n%s", sh, err, out.String())
		}
		exe := map[string]string{"powershell": "pwsh"}[sh]
		if exe == "" {
			exe = sh
		}
		if _, err := exec.LookPath(exe); err != nil || sh == "powershell" {
			continue // pwsh has no syntax-only check
		}
		f := filepath.Join(t.TempDir(), "script")
		os.WriteFile(f, []byte(out.String()), 0o644)
		if b, err := exec.Command(exe, "-n", f).CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v\n%s", sh, err, b)
		}
	}
	if err := run([]string{"completion", "tcsh"}, &strings.Builder{}); err == nil {
		t.Error("completion tcsh: no error")
	}
}

// TestCompletionHelper is the program for TestBashCompletion; it does nothing
// when run normally.
func TestCompletionHelper(t *testing.T) {
	if os.Getenv("ZB_COMPLETION_HELPER") == "" {
		return
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	run(args, os.Stdout)
	os.Exit(0)
}

// TestBashCompletion runs the bash script's function as bash calls it on Tab
// (on macOS CI with bash 3.2).
func TestBashCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no bash")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	cfg, root := completionSetup(t)
	prog := filepath.Join(t.TempDir(), "zotero-backup")
	os.WriteFile(prog, []byte("#!/bin/sh\nexec \""+os.Args[0]+"\" -test.run='^TestCompletionHelper$' -- \"$@\"\n"), 0o755)
	var script strings.Builder
	run([]string{"completion", "bash"}, &script)
	test := script.String() + `
t() { COMP_WORDS=("$@"); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); COMPREPLY=(); _zotero_backup; echo "${COMPREPLY[*]}"; }
t "$PROG" re
t "$PROG" --config "$CFG" backup --exclude files,
t "$PROG" --config "$CFG" restore "$ROOT/u"
`
	cmd := exec.Command("bash", "-c", test)
	cmd.Env = append(os.Environ(), "ZB_COMPLETION_HELPER=1", "PROG="+prog, "CFG="+cfg, "ROOT="+root)
	b, err := cmd.CombinedOutput()
	want := "restore\nfiles,annotations files,attachments files,fulltext files,notes files,settings files,trash\n" + filepath.Join(root, "user-7-me") + "/\n"
	if err != nil || string(b) != want {
		t.Errorf("%v\n got %q\nwant %q", err, b, want)
	}
}
