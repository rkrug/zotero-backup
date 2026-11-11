package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// completeCommands are the commands completion offers (not the hidden
// stresstest), each with the words its first argument can be.
var completeCommands = map[string][]string{
	"backup": nil, "list": nil, "diff": nil, "restore": nil, "find": nil, "show": nil,
	"schedule": {"install", "show", "remove"}, "notify-test": nil, "verify": nil,
	"status": nil, "stop": nil, "prune": nil, "key": {"check", "set", "remove"},
	"config": nil, "version": nil, "upgrade": {"stable", "beta"},
	"completion": {"zsh", "bash", "fish", "powershell"},
}

// flagCommands reads the commands a flag is for from its usage text
// ("restore, upgrade: …"); ok is false for a flag of every command.
func flagCommands(usage string) (cmds []string, ok bool) {
	before, _, found := strings.Cut(usage, ":")
	if !found {
		return nil, false
	}
	for _, part := range strings.FieldsFunc(before, func(r rune) bool { return r == ',' || r == '/' }) {
		f := strings.Fields(part)
		if len(f) == 0 {
			return nil, false
		}
		if _, known := completeCommands[f[0]]; !known && f[0] != "stresstest" {
			return nil, false
		}
		cmds = append(cmds, f[0])
	}
	return cmds, len(cmds) > 0
}

// complete prints the candidates for the last word of args, one per line,
// for the scripts of `completion`. The last word comes with a leading "+"
// (Windows PowerShell drops empty arguments). It never reads an API key or
// contacts the server, and prints nothing rather than an error; no output
// lets the shell complete file names.
func complete(fs *flag.FlagSet, args []string, out io.Writer) {
	if len(args) == 0 {
		return
	}
	cur := strings.TrimPrefix(args[len(args)-1], "+")
	words := args[:len(args)-1]
	cfgPath, profileName := fs.Lookup("config").DefValue, "default"
	var pos []string
	pending := "" // a flag waiting for its value
	for _, w := range words {
		switch {
		case pending != "":
			if pending == "config" {
				cfgPath = expandHome(w)
			} else if pending == "profile" {
				profileName = w
			}
			pending = ""
		case w == "--":
			pending = ""
		case strings.HasPrefix(w, "-") && len(w) > 1:
			name, value, hasValue := strings.Cut(strings.TrimLeft(w, "-"), "=")
			f := fs.Lookup(name)
			if f == nil {
				continue
			}
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				continue
			}
			if !hasValue {
				pending = name
			} else if name == "config" {
				cfgPath = expandHome(value)
			} else if name == "profile" {
				profileName = value
			}
		default:
			pos = append(pos, w)
		}
	}
	profile := func() (Profile, bool) {
		cfg, err := loadConfig(cfgPath)
		p, ok := cfg.Profiles[profileName]
		return p, err == nil && ok
	}
	var cands []string
	switch {
	case pending != "":
		cands = flagValues(pending, cur, cfgPath, profile)
	case strings.HasPrefix(cur, "-"):
		cmd := ""
		if len(pos) > 0 {
			cmd = pos[0]
		}
		fs.VisitAll(func(f *flag.Flag) {
			cmds, only := flagCommands(f.Usage)
			if cmd != "stresstest" && (!only || slices.Contains(cmds, cmd)) {
				cands = append(cands, "--"+f.Name)
			}
		})
	case len(pos) == 0:
		for c := range completeCommands {
			cands = append(cands, c)
		}
	default:
		cands = argValues(pos, cur, profile)
	}
	slices.Sort(cands)
	for _, c := range cands {
		if strings.HasPrefix(c, cur) {
			fmt.Fprintln(out, c)
		}
	}
}

func flagValues(name, cur, cfgPath string, profile func() (Profile, bool)) []string {
	switch name {
	case "profile":
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			return nil
		}
		return sortedKeys(cfg.Profiles)
	case "exclude": // a comma-separated list: complete its last part
		done := cur[:strings.LastIndex(cur, ",")+1]
		var c []string
		for _, e := range excludable {
			if !slices.Contains(splitList(done), e) {
				c = append(c, done+e)
			}
		}
		return c
	case "progress":
		return []string{"auto", "line", "log", "quiet"}
	case "key":
		if p, ok := profile(); ok {
			return sortedKeys(p.keyNames())
		}
	}
	return nil
}

func argValues(pos []string, cur string, profile func() (Profile, bool)) []string {
	cmd, n := pos[0], len(pos) // n: the number of the argument being completed
	if words := completeCommands[cmd]; n == 1 && words != nil {
		return words
	}
	switch {
	case cmd == "schedule" && n == 2 && pos[1] == "show":
		return []string{"raw"}
	case cmd == "key" && n == 2:
		if p, ok := profile(); ok {
			return sortedKeys(p.keyNames())
		}
		return nil
	}
	p, ok := profile()
	if !ok {
		return nil
	}
	root := expandHome(p.To)
	switch {
	case (cmd == "restore" || cmd == "show") && n == 1, cmd == "diff" && n <= 2:
		return backupPaths(root, cur, true)
	case cmd == "verify" && n == 1:
		return append(backupPaths(root, cur, true), "all")
	case cmd == "find" && n == 2:
		return backupPaths(root, cur, false)
	case cmd == "stop" && n == 1:
		var c []string
		for _, l := range libraryFolders(root) {
			kind, id, _ := strings.Cut(l, "-")
			if kind == "group" {
				id, _, _ = strings.Cut(id, "-")
				c = append(c, "group:"+id)
			} else if !slices.Contains(c, "user") {
				c = append(c, "user")
			}
		}
		return c
	}
	return nil
}

// libraryFolders lists the folders in root that hold backups.
func libraryFolders(root string) []string {
	entries, _ := os.ReadDir(root)
	var libs []string
	for _, e := range entries {
		if e.IsDir() && (strings.HasPrefix(e.Name(), "user-") || strings.HasPrefix(e.Name(), "group-")) &&
			len(backups(filepath.Join(root, e.Name()))) > 0 {
			libs = append(libs, e.Name())
		}
	}
	return libs
}

// backupPaths gives the library folders in root (ending in the separator, to
// go on typing) and, once cur is inside one, its backups. A cur starting
// with ~ gets the paths with ~ too, one with / (on Windows) paths with /.
func backupPaths(root, cur string, withBackups bool) []string {
	sep := string(filepath.Separator)
	home, _ := os.UserHomeDir()
	show := func(p string) string {
		if strings.HasPrefix(cur, "~") && home != "" && strings.HasPrefix(p, home+sep) {
			p = "~" + p[len(home):]
		}
		if strings.Contains(cur, "/") {
			p = filepath.ToSlash(p)
		}
		return p
	}
	var c []string
	for _, l := range libraryFolders(root) {
		dir := filepath.Join(root, l)
		c = append(c, show(dir+sep))
		if withBackups && strings.HasPrefix(cur, show(dir+sep)) {
			for _, b := range backups(dir) {
				c = append(c, show(filepath.Join(dir, b)))
			}
		}
	}
	return c
}

// completionScript is the script `completion SHELL` prints. Each asks the
// program itself for the candidates, so an upgrade needs no new script.
func completionScript(shell string) (string, error) {
	switch shell {
	case "zsh":
		return `#compdef zotero-backup
# zotero-backup completion for zsh: source <(zotero-backup completion zsh)
_zotero-backup() {
  local -a c d o
  c=("${(@f)$("${words[1]/#\~/$HOME}" __complete "${(@Q)words[2,CURRENT-1]}" "+${(Q)words[CURRENT]}" 2>/dev/null)}")
  c=(${c:#})
  if (( ${#c} == 0 )); then _files; return; fi
  d=(${(M)c:#*/}) o=(${c:#*/})
  (( ${#d} )) && compadd -S '' -- $d
  (( ${#o} )) && compadd -- $o
}
if [[ "$funcstack[1]" == "_zotero-backup" ]]; then _zotero-backup "$@"; else compdef _zotero-backup zotero-backup; fi
`, nil
	case "bash":
		return `# zotero-backup completion for bash: eval "$(zotero-backup completion bash)" in ~/.bashrc
_zotero_backup() {
  local cur words cword
  if declare -F _get_comp_words_by_ref >/dev/null; then
    _get_comp_words_by_ref -n =: cur words cword
  else
    cur=${COMP_WORDS[COMP_CWORD]} words=("${COMP_WORDS[@]}") cword=$COMP_CWORD
  fi
  local args=("${words[@]:1:cword-1}") # before IFS changes: bash 3.2 would join the slice
  local IFS=$'\n'
  COMPREPLY=($("${words[0]/#\~/$HOME}" __complete "${args[@]}" "+$cur" 2>/dev/null))
  declare -F __ltrim_colon_completions >/dev/null && __ltrim_colon_completions "$cur"
  if [[ ${#COMPREPLY[@]} -eq 1 && ${COMPREPLY[0]} == */ ]] && type compopt &>/dev/null; then compopt -o nospace 2>/dev/null; fi
}
complete -o default -F _zotero_backup zotero-backup
`, nil
	case "fish":
		return `# zotero-backup completion for fish: zotero-backup completion fish > ~/.config/fish/completions/zotero-backup.fish
function __zotero_backup_complete
    set -l words (commandline -opc)
    set -l cmd $words[1]
    set -e words[1]
    set -l cur (commandline -ct)
    set -l c ($cmd __complete $words "+$cur" 2>/dev/null)
    if test (count $c) -eq 0
        __fish_complete_path "$cur"
    else
        printf '%s\n' $c
    end
end
complete -c zotero-backup -f -a '(__zotero_backup_complete)'
`, nil
	case "powershell":
		return `# zotero-backup completion for PowerShell: zotero-backup completion powershell >> $PROFILE
Register-ArgumentCompleter -Native -CommandName zotero-backup, zotero-backup.exe -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $words = @($commandAst.CommandElements | Where-Object { $_.Extent.EndOffset -lt $cursorPosition } | ForEach-Object {
        if ($_ -is [System.Management.Automation.Language.StringConstantExpressionAst]) { $_.Value } else { $_.Extent.Text }
    })
    $rest = @(if ($words.Count -gt 1) { $words[1..($words.Count - 1)] })
    & $words[0] __complete @rest "+$wordToComplete" 2>$null | Where-Object { $_ } | ForEach-Object {
        $text = if ($_ -match '[\s''"]') { "'" + ($_ -replace "'", "''") + "'" } else { $_ }
        [System.Management.Automation.CompletionResult]::new($text, $_, 'ParameterValue', $_)
    }
}
`, nil
	}
	return "", fmt.Errorf("completion: %q is not zsh, bash, fish or powershell", shell)
}
