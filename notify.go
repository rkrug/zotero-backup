package main

// Notifications when a backup fails (#35): a command of the user's choice and,
// on macOS, a system notification. Neither ever sees an API key.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// backupAndNotify runs the backup and, when it failed or reported problems,
// sends the profile's notifications. A failing notification is reported on
// stderr and does not change the backup's result.
func backupAndNotify(clientFor func(string) (*Client, error), p Profile, out, errOut io.Writer, pr *progress) error {
	sum, err := runBackup(clientFor, p, out, pr)
	if sum.Status == "ok" || (len(p.NotifyCommand) == 0 && !p.NotifyMacOS) {
		return err
	}
	text := []string{sum.line()}
	var failedLibs []string
	for _, r := range sum.Libraries {
		text = append(text, libLine(r))
		if r.Result == "failed" {
			failedLibs = append(failedLibs, r.label())
		}
	}
	if err != nil {
		text = append(text, "zotero-backup: "+err.Error())
	}
	for _, e := range notify(p, sum.Status, strings.Join(text, "\n")+"\n", failedLibs) {
		fmt.Fprintf(errOut, "zotero-backup: notification failed: %v\n", e)
	}
	return err
}

// notify sends the notifications; it returns their errors.
func notify(p Profile, status, summary string, failedLibs []string) []error {
	var errs []error
	if len(p.NotifyCommand) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, p.NotifyCommand[0], p.NotifyCommand[1:]...)
		cmd.Stdin = strings.NewReader(summary)
		cmd.Env = append(withoutKey(os.Environ()), "ZB_STATUS="+status, "ZB_LIBRARIES_FAILED="+strings.Join(failedLibs, " "))
		if b, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v %s", p.NotifyCommand[0], err, strings.TrimSpace(string(b))))
		}
	}
	if p.NotifyMacOS {
		if runtime.GOOS != "darwin" {
			errs = append(errs, errors.New("notify_macos: only on macOS"))
		} else {
			msg := "Backup " + status
			if len(failedLibs) > 0 {
				msg += ": " + strings.Join(failedLibs, ", ")
			}
			// the text goes in as an argument, never as AppleScript source
			err := exec.Command("osascript", "-e", "on run argv", "-e", `display notification (item 1 of argv) with title "zotero-backup"`, "-e", "end run", msg).Run()
			if err != nil {
				errs = append(errs, fmt.Errorf("osascript: %v", err))
			}
		}
	}
	return errs
}

// withoutKey drops ZOTERO_API_KEY from an environment.
func withoutKey(env []string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, "ZOTERO_API_KEY=") {
			out = append(out, e)
		}
	}
	return out
}

// notifyTest sends a test notification with the profile's settings.
func notifyTest(p Profile, out io.Writer) error {
	if len(p.NotifyCommand) == 0 && !p.NotifyMacOS {
		return errors.New("the profile has no notify_command and no notify_macos")
	}
	if errs := notify(p, "test", "zotero-backup: test notification\n", nil); len(errs) > 0 {
		return errors.Join(errs...)
	}
	fmt.Fprintln(out, "test notification sent")
	return nil
}
