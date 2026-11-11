package main

// runChecks (part of the extras): whole backup runs as the backup command
// makes them, into a folder of their own below the run's backups: the summary
// line, last-run.json and the result files, the run logs and their archiving
// by prune, a backup folder that does not exist, a library locked by another
// run, a file missing on the server (new, then not new), a stop in the middle
// of a run, the notification, and libraries the key may not read.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// zoteroKey makes a new object key as Zotero does (8 characters, no 0, 1, O).
func zoteroKey() string {
	const alphabet = "23456789ABCDEFGHIJKLMNPQRSTUVWXYZ"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// stressSelf is the program the stop check starts (replaced in tests).
var stressSelf = os.Executable

// failItems makes every read of a library's items fail (a library that cannot be read).
type failItems struct{}

func (failItems) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.Contains(r.URL.Path, "/items") {
		return nil, errors.New("connection reset by peer (simulated)")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// watchWriter collects a program's output and closes seen once want appears.
type watchWriter struct {
	mu   sync.Mutex
	want string
	buf  strings.Builder
	seen chan struct{}
	once sync.Once
}

func (w *watchWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(b)
	if strings.Contains(w.buf.String(), w.want) {
		w.once.Do(func() { close(w.seen) })
	}
	return len(b), nil
}

// lastLine is the last line of a command's output (its summary line).
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndex(s, "\n")+1:]
}

func (s *stress) runChecks() {
	const sc, op = "extras", "runs"
	root := filepath.Join(s.root, "runs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.record(sc, op, "make the folder", false, "%v", err)
		return
	}
	p := s.profile
	p.Library, p.To, p.LogDir, p.Exclude = s.lib.Spec(), root, "logs", nil
	p.NotifyCommand, p.NotifyMacOS = nil, false
	logs := filepath.Join(root, "logs")
	client := fixedClient(s.c)
	run := func(p Profile, c func(string) (*Client, error)) (*runSummary, string, error) {
		time.Sleep(1100 * time.Millisecond) // backups and run logs are named by the second
		var out strings.Builder
		sum, err := runBackup(c, p, &out, quietProgress())
		return sum, out.String(), err
	}

	// 1. a backup folder that does not exist: refused, nothing created
	gone := p
	gone.To = filepath.Join(root, "not-mounted", "zotero-backups")
	_, _, err := run(gone, client)
	_, serr := os.Stat(filepath.Dir(gone.To))
	s.record(sc, op, "a backup folder that does not exist is refused, nothing is created", err != nil && strings.Contains(err.Error(), "does not exist") && serr != nil, "%v", err)

	// 2. a library that cannot be read: failed run, log and result file, notification
	notified := filepath.Join(s.root, "notified-"+s.lib.ID)
	failing := p
	if runtime.GOOS != "windows" {
		failing.NotifyCommand = []string{"/bin/sh", "-c", `cat > "$0"; echo "--env--" >> "$0"; env >> "$0"`, notified}
	}
	bad := &Client{Base: s.c.Base, Key: s.c.Key, Attempts: 2, Sleep: func(time.Duration) {}, HTTP: &http.Client{Transport: failItems{}, Timeout: time.Minute}}
	time.Sleep(1100 * time.Millisecond)
	var out strings.Builder
	err = backupAndNotify(fixedClient(bad), failing, &out, io.Discard, quietProgress())
	r1 := &runSummary{}
	rerr := readJSON(filepath.Join(root, lastRunName), r1)
	s.record(sc, op, "a library that cannot be read: 'backup failed: 1 libraries (0 backed up, 1 failed, 0 skipped)', exit status 1, last-run.json failed",
		err != nil && rerr == nil && r1.Status == "failed" && r1.ExitStatus == 1 &&
			strings.HasPrefix(lastLine(out.String()), "backup failed: 1 libraries (0 backed up, 1 failed, 0 skipped)"), "%v\n%s", err, out.String())
	_, lerr := os.Stat(r1.Log)
	_, jerr := os.Stat(filepath.Join(logs, runLogName(r1.Started, "backup", ".json")))
	s.record(sc, op, "the failed run has its log and result file in logs/", r1.Log != "" && lerr == nil && jerr == nil, "log %q: %v, result: %v", r1.Log, lerr, jerr)
	if runtime.GOOS != "windows" {
		b, nerr := os.ReadFile(notified)
		text, env, _ := strings.Cut(string(b), "--env--")
		s.record(sc, op, "the notification starts with the summary line, names the log, ZB_STATUS=failed, no API key",
			nerr == nil && strings.HasPrefix(text, "backup failed: 1 libraries") && strings.Contains(text, "log: "+r1.Log) &&
				strings.Contains(env, "ZB_STATUS=failed") && !strings.Contains(string(b), s.c.Key), "%v\n%s", nerr, text)
		os.Remove(notified)
	}

	// 3. a normal run: summary line, last-run.json, result file and log agree with the backup
	sum2, out2, err := run(p, client)
	b2 := ""
	if len(sum2.Libraries) == 1 {
		b2 = sum2.Libraries[0].Backup
	}
	var m2 Meta
	merr := readJSON(filepath.Join(b2, "meta.json"), &m2)
	s.record(sc, op, "a backup run: 'backup finished: 1 libraries (1 backed up, 0 failed, 0 skipped)', exit status 0; its counts are those of the backup",
		err == nil && merr == nil && sum2.Status == "ok" && sum2.ExitStatus == 0 &&
			strings.HasPrefix(lastLine(out2), "backup finished: 1 libraries (1 backed up, 0 failed, 0 skipped)") &&
			sum2.Libraries[0].Items == itemCount(m2.Counts) && sum2.Libraries[0].Files == m2.Counts["files"] && sum2.Missing == len(m2.Missing),
		"%v %v\n%s", err, merr, out2)
	if b2 == "" {
		return // the rest builds on this backup
	}
	r := (&verifier{seen: map[string][]os.FileInfo{}}).check(b2)
	s.record("backups", "verify", "backup "+filepath.Base(b2)+" (runs) passes verify", len(r.problems) == 0, "%s", strings.Join(r.problems, "; "))
	lb, _ := os.ReadFile(sum2.Log)
	log := string(lb)
	s.record(sc, op, "the run log has the progress, the library's line and the summary line, with times",
		strings.Contains(log, s.lib.Dir()+": items") && strings.Contains(log, " "+libLine(sum2.Libraries[0])) && strings.Contains(log, " "+sum2.line()) &&
			strings.Contains(log, "resuming "), "%s", log)

	// 4. a file missing on the server (an attachment whose file was never uploaded): new once, then not
	parent, att := zoteroKey(), zoteroKey()
	res, err := s.c.writeObjects(s.lib.Path, "items", []map[string]any{
		{"key": parent, "version": 0, "itemType": "book", "title": "[zb-e2e] " + s.run + " missing file",
			"tags": []map[string]any{{"tag": "zb-e2e"}, {"tag": "zb-e2e-run-" + s.run + "-runs"}}},
		{"key": att, "version": 0, "itemType": "attachment", "parentItem": parent, "linkMode": "imported_file",
			"title": "never uploaded", "filename": "zb-e2e-never-uploaded.pdf", "contentType": "application/pdf"}})
	if err == nil && len(res.Written) != 2 {
		err = fmt.Errorf("not written: %v", res.Failed)
	}
	if err != nil {
		att = ""
	}
	if err != nil {
		s.record(sc, op, "create an attachment without its file", false, "%v", err)
	} else {
		sum3, out3, err := run(p, client)
		nm := []string{}
		if len(sum3.Libraries) == 1 {
			for _, f := range sum3.Libraries[0].NewMissing {
				nm = append(nm, f.Key)
			}
		}
		s.record(sc, op, "a file missing on the server is reported as new: '(1 new)', listed in last-run.json, status still ok",
			err == nil && sum3.Status == "ok" && sum3.NewMissing == 1 && len(nm) == 1 && nm[0] == att && sum3.Missing == sum2.Missing+1 &&
				strings.Contains(lastLine(out3), fmt.Sprintf("%d missing files (1 new)", sum3.Missing)), "%v %v\n%s", err, nm, out3)
	}

	// 5. a library locked by another run: skipped, not failed, exit status 0
	lib := filepath.Dir(b2)
	if release, err := acquireLock(lib, s.pr.Warnf); err != nil {
		s.record(sc, op, "take the library's lock", false, "%v", err)
	} else {
		sum4, out4, err := run(p, client)
		release()
		s.record(sc, op, "a library locked by another run is skipped, not failed: '(0 backed up, 0 failed, 1 skipped)', 'running: PID', exit status 0",
			err == nil && sum4.Status == "ok" && sum4.ExitStatus == 0 && sum4.Skipped == 1 &&
				strings.Contains(lastLine(out4), "(0 backed up, 0 failed, 1 skipped)") && strings.Contains(lastLine(out4), "running: PID"), "%v\n%s", err, out4)
	}

	// 6. the next run: the missing file is no longer new
	if att != "" {
		sum5, out5, err := run(p, client)
		s.record(sc, op, "the next run: the missing file is no longer new '(0 new)'",
			err == nil && sum5.NewMissing == 0 && sum5.Missing == sum2.Missing+1, "%v\n%s", err, lastLine(out5))
		if err := s.api.delete("items", parent); err != nil {
			s.record(sc, op, "erase the item with the missing file", false, "%v", err)
		}
		s.sync(sc, "sync after the missing-file item", "fail")
	}

	// 7. prune: all but the newest backup go; the logs of runs whose backups are gone, and of
	// runs that made none (before a newer success), move to logs/archive
	pp := p
	pp.KeepDaily, pp.KeepWeekly, pp.KeepMonthly = 1, 0, 0
	time.Sleep(1100 * time.Millisecond)
	out.Reset()
	err = runPrune(root, pp, false, &out)
	left, _ := os.ReadDir(logs)
	archived, _ := os.ReadDir(filepath.Join(logs, "archive"))
	var leftNames []string
	for _, e := range left {
		if !e.IsDir() {
			leftNames = append(leftNames, e.Name())
		}
	}
	newest := ""
	if bs := backups(lib); len(bs) > 0 {
		newest = bs[len(bs)-1]
	}
	last := &runSummary{}
	readJSON(filepath.Join(root, lastRunName), last)
	keptRun := runLogName(last.Started, "backup", "")
	ok := err == nil && len(backups(lib)) == 1 && len(leftNames) == 3 && len(archived) >= 6 && last.Libraries[0].Backup == filepath.Join(lib, newest)
	for _, n := range leftNames {
		ok = ok && (strings.HasPrefix(n, keptRun) || strings.HasSuffix(n, "-prune.log"))
	}
	s.record(sc, op, "prune: one backup left; logs/ keeps the newest run's log and result and the prune log, the other runs' logs are in logs/archive",
		ok, "%v; left %v, %d archived\n%s", err, leftNames, len(archived), out.String())

	// 8. a stop in the middle of a run (the program itself, Ctrl-C): 'backup stopped', the next run continues
	s.stopCheck(root, p, lib)

	// 9. libraries the key may not read are counted apart ("N without access")
	s.noAccessCheck(root, p)
}

// stopCheck starts the program with backup and sends it Ctrl-C once the
// library is being read.
func (s *stress) stopCheck(root string, p Profile, lib string) {
	const sc, op = "extras", "runs"
	if runtime.GOOS == "windows" {
		s.note(sc, op, "stop", "not on Windows (no Ctrl-C signal to another process; zotero-backup stop ends it hard)")
		return
	}
	exe, err := stressSelf()
	cfg := filepath.Join(s.root, "runs-config-"+s.lib.ID+".json")
	if err == nil {
		err = writeJSON(cfg, Config{Profiles: map[string]Profile{"default": {Library: p.Library, Key: p.forRestore().keyName(p.Library),
			Keys: map[string]string{}, To: root, Exclude: []string{}, LogDir: "logs", KeepDaily: 7, KeepWeekly: 5, KeepMonthly: 12,
			Progress: "log", ProgressInterval: 30, NotifyCommand: []string{}}}})
	}
	if err != nil {
		s.record(sc, op, "stop: prepare", false, "%v", err)
		return
	}
	defer os.Remove(cfg)
	time.Sleep(1100 * time.Millisecond)
	cmd := exec.Command(exe, "backup", "--config", cfg)
	var stdout strings.Builder
	stderr := &watchWriter{want: ": items", seen: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	if err := cmd.Start(); err != nil {
		s.record(sc, op, "stop: start the program", false, "%v", err)
		return
	}
	select {
	case <-stderr.seen: // the library is being read
	case <-time.After(2 * time.Minute):
	}
	cmd.Process.Signal(os.Interrupt)
	err = cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	last := &runSummary{}
	readJSON(filepath.Join(root, lastRunName), last)
	_, lockErr := os.Stat(filepath.Join(lib, lockName))
	s.record(sc, op, "stop: 'backup stopped: … ; stopped: <library>', exit status 1, last-run.json stopped, no lock left, a .partial for the next run",
		code == 1 && strings.HasPrefix(lastLine(stdout.String()), "backup stopped: 1 libraries") && strings.Contains(stdout.String(), "; stopped: "+s.lib.ID) &&
			last.Status == "stopped" && lockErr != nil && findPartial(lib) != "", "%v, exit %d\n%s", err, code, stdout.String())

	time.Sleep(1100 * time.Millisecond)
	var out strings.Builder
	sum, err := runBackup(fixedClient(s.c), p, &out, quietProgress())
	lb, _ := os.ReadFile(sum.Log)
	s.record(sc, op, "stop: the next run continues the unfinished backup",
		err == nil && sum.BackedUp == 1 && strings.Contains(string(lb), "resuming ") && findPartial(lib) == "", "%v\n%s", err, out.String())
}

// noAccessCheck backs up "all" when every library the key can read is one it
// may write to (so a test library): the others are "without access".
func (s *stress) noAccessCheck(root string, p Profile) {
	const sc, op = "extras", "runs"
	readable, skipped, err := resolveLibraries(s.c, "all")
	writable, werr := writableLibraries(s.c)
	if err != nil || werr != nil {
		s.record(sc, op, "without access: list the libraries", false, "%v %v", err, werr)
		return
	}
	test := map[string]bool{}
	for _, l := range writable {
		test[l.ID] = true
	}
	var other []string
	for _, l := range readable {
		if !test[l.ID] {
			other = append(other, l.Label())
		}
	}
	noAccess := 0
	for _, x := range skipped {
		if strings.HasSuffix(x, ": no access") {
			noAccess++
		}
	}
	if len(other) > 0 || noAccess == 0 {
		s.note(sc, op, "without access", "not checked: the key can also read %v, or there is no library it may not read (%d)", other, noAccess)
		return
	}
	all := p
	all.Library, all.To = "all", filepath.Join(root, "all")
	os.MkdirAll(all.To, 0o755)
	time.Sleep(1100 * time.Millisecond)
	var out strings.Builder
	sum, err := runBackup(fixedClient(s.c), all, &out, quietProgress())
	want := fmt.Sprintf("backup finished: %d libraries (%d backed up, 0 failed, 0 skipped), %d without access,", len(readable), len(readable), noAccess)
	s.record(sc, op, "library all: the libraries the key may not read are counted apart ('N without access'), not as skipped",
		err == nil && sum.NoAccess == noAccess && strings.HasPrefix(lastLine(out.String()), want), "%v; want %q\n%s", err, want, lastLine(out.String()))
}
