package main

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testScheduler(t *testing.T, goos string) (*scheduler, *[]string, *strings.Builder) {
	var calls []string
	var out strings.Builder
	s := newScheduler(goos, t.TempDir(), "/opt/my tools/zotero-backup", "test", "/cfg dir/c.json", &out)
	s.uid = "501"
	s.run = func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "status of " + s.name + "\n", nil
	}
	return s, &calls, &out
}

func TestScheduleInstallShowRemove(t *testing.T) {
	cases := []struct {
		goos        string
		files       []string // relative to home
		content     []string // in the files
		installCall []string
		removeCall  string
	}{
		{"darwin", []string{"Library/LaunchAgents/org.zotero-backup.test.plist"},
			[]string{"<string>org.zotero-backup.test</string>", "<integer>2</integer>", "<integer>30</integer>",
				`'/opt/my tools/zotero-backup' 'backup' '--profile' 'test' '--config' '/cfg dir/c.json' '--progress' 'log' &amp;&amp; '/opt/my tools/zotero-backup' 'prune'`,
				filepath.Join("Library", "Logs", "zotero-backup.log")},
			[]string{"launchctl bootout gui/501/org.zotero-backup.test", "launchctl bootstrap gui/501 "}, "launchctl bootout gui/501/org.zotero-backup.test"},
		{"linux", []string{".config/systemd/user/zotero-backup-test.service", ".config/systemd/user/zotero-backup-test.timer"},
			[]string{`ExecStart="/opt/my tools/zotero-backup" "backup" "--profile" "test" "--config" "/cfg dir/c.json" "--progress" "log"`,
				`ExecStartPost="/opt/my tools/zotero-backup" "prune"`, "OnCalendar=*-*-* 02:30:00", "Persistent=true"},
			[]string{"systemctl --user daemon-reload", "systemctl --user enable --now zotero-backup-test.timer"}, "systemctl --user disable --now zotero-backup-test.timer"},
		{"windows", []string{"AppData/Local/zotero-backup/zotero-backup-test.cmd"},
			[]string{`"/opt/my tools/zotero-backup" "backup" "--profile" "test"`, "if errorlevel 1 exit /b 1", `"prune"`, "zotero-backup.log"},
			[]string{"schtasks /Create /F /SC DAILY /ST 02:30 /TN zotero-backup-test /TR \""}, "schtasks /Delete /TN zotero-backup-test /F"},
	}
	for _, c := range cases {
		t.Run(c.goos, func(t *testing.T) {
			s, calls, out := testScheduler(t, c.goos)
			if err := s.install("2:30"); err != nil {
				t.Fatal(err)
			}
			var all string
			for _, f := range c.files {
				b, err := os.ReadFile(filepath.Join(s.home, filepath.FromSlash(f)))
				if err != nil {
					t.Fatal(err)
				}
				all += string(b)
				if !strings.Contains(string(b), scheduleMark) {
					t.Errorf("%s lacks the mark", f)
				}
			}
			for _, want := range c.content {
				if !strings.Contains(all, want) {
					t.Errorf("files lack %q:\n%s", want, all)
				}
			}
			if c.goos == "darwin" {
				if err := xml.Unmarshal([]byte(all), new(struct{})); err != nil {
					t.Errorf("plist is no XML: %v", err)
				}
			}
			got := strings.Join(*calls, "\n")
			for _, want := range c.installCall {
				if !strings.Contains(got, want) {
					t.Errorf("calls lack %q:\n%s", want, got)
				}
			}
			out.Reset()
			if err := s.show(); err != nil || !strings.Contains(out.String(), "status of "+s.name) || !strings.Contains(out.String(), scheduleMark) {
				t.Errorf("show: %v\n%s", err, out.String())
			}
			if err := s.install("03:00"); err != nil { // again: replaces its own files
				t.Errorf("second install: %v", err)
			}
			if err := s.remove(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(*calls, "\n"), c.removeCall) {
				t.Errorf("remove calls:\n%s", strings.Join(*calls, "\n"))
			}
			if len(s.installed()) != 0 {
				t.Error("files left after remove")
			}
			if err := s.remove(); err == nil {
				t.Error("remove with nothing installed")
			}
		})
	}
}

func TestScheduleLeavesHandMadeFilesAlone(t *testing.T) {
	s, calls, _ := testScheduler(t, "darwin")
	p := s.plistPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("<plist>my own</plist>"), 0o644)
	for name, f := range map[string]func() error{"install": func() error { return s.install("06:00") }, "remove": s.remove} {
		if err := f(); err == nil || !strings.Contains(err.Error(), "not written by zotero-backup schedule") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "<plist>my own</plist>" || len(*calls) != 0 {
		t.Errorf("hand-made file touched: %q, calls %v", b, *calls)
	}
}

func TestScheduleRefusals(t *testing.T) {
	s, _, _ := testScheduler(t, "linux")
	for _, at := range []string{"24:00", "6", "06:60", "6pm"} {
		if err := s.install(at); err == nil {
			t.Errorf("--at %q accepted", at)
		}
	}
	s.exe = "/tmp/go-build123/b001/exe/zotero-backup"
	if err := s.install("06:00"); err == nil || !strings.Contains(err.Error(), "temporary folder") {
		t.Errorf("go run binary: %v", err)
	}
	d := newScheduler("darwin", "/h", "/x", "default", "", nil)
	if d.name != "org.zotero-backup" || len(d.args) != 0 {
		t.Errorf("default profile: %s %v", d.name, d.args)
	}
}

func TestShJoinQuotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	args := []string{"printf", `%s|`, "it's", `a "b" $HOME`, "x && y"}
	b, err := exec.Command("/bin/sh", "-c", shJoin(args)).Output()
	if err != nil || string(b) != `it's|a "b" $HOME|x && y|` {
		t.Errorf("%q %v", b, err)
	}
}
