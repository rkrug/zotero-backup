---
name: zotero-backup
description: Back up, list, compare, restore and prune Zotero libraries (personal library and groups) with the zotero-backup command-line tool, safely. Use whenever the user wants to back up a Zotero library, look at or compare backups, restore items or a whole library from a backup, clean up old backups, set up or check zotero-backup's API keys, check a scheduled backup run, or update zotero-backup itself.
---

# zotero-backup

`zotero-backup` backs up Zotero libraries through the Zotero web API and restores them **into the same library, keeping the item keys** (citations in Word, LibreOffice and Google Docs keep working). Documentation: <https://rkrug.github.io/zotero-backup/> (version menu in the header; match it to `zotero-backup version`). `zotero-backup --help` lists every command and option. Not installed (`command not found`): point the user to the documentation's Install page. On macOS the first start of a downloaded binary is blocked (not notarised): the user allows it in System Settings → Privacy & Security; do not remove the quarantine flag on your own.

- Every backup is a **complete**, dated folder: `<backups>/<group-ID-Name | user-ID-username>/<YYYY-MM-DDTHHMMSSZ>/` (UTC). Unchanged files are **hard links** to the previous backup: never edit files inside a backup, and copy or move a backup tree only with a tool that keeps hard links (`rsync -aH`), or every backup becomes a full copy. The first backup downloads every file and can take long; later runs download only new and changed files (but read all metadata).
- `restore` sets everything in the backup back to its state then, and **never deletes** objects created after the backup (unless `--trash-new` / `--erase-new`). Changes made since the backup to objects in the backup are lost; a field that was empty in the backup is cleared again.
- Without `--items`, restore also sets the **library settings back as a whole**: tag colours assigned after the backup are removed. Say so when the plan lists `settings to set back`.
- Restored files count against the library owner's **Zotero storage**; a plan with many `files to upload` can hit the quota.
- Text files and web-page snapshots are kept as `<name>.zip` in the backup (Zotero's own upload form): unzip a copy to open one; restore uploads the ZIP as it is.
- Not covered: linked files, Zotero desktop's own data (plugins, styles, preferences), restoring into a *different* library.

## Safety rules (always)

1. **API keys:** never ask for, print, log, store, echo or pipe an API key, and never put one on a command line, in an environment variable or in a file. The user stores keys themselves by typing `zotero-backup key set [NAME]` in their own terminal (input hidden), or sets `ZOTERO_API_KEY` themselves. You may run `zotero-backup key check [NAME]` (it shows no key). **A key pasted into the chat** is exposed: do not use it; tell the user to delete it at <https://www.zotero.org/settings/keys>, create a new one and store that with `key set`.
2. **`restore` changes the user's Zotero library.** Before any restore:
   - the user names the backup (folder or library) to restore; never pick a library they did not name;
   - remind them to **sync every Zotero app** that works on the library first (otherwise the app shows its conflict window for each object the restore changed, and the choice made there wins), and to sync again afterwards: Zotero desktop shows the result only after its next sync;
   - run `zotero-backup restore BACKUP --dry-run` and show the whole plan;
   - get an explicit **yes to the plan you just showed**. Only then run the same command (same backup, `--items`, `--key`) with `--yes` instead of `--dry-run` (without a terminal, restore refuses to run without `--yes`), or let the user run `zotero-backup restore BACKUP` in their own terminal, where it asks. A vague "ok" to something else is not a yes to the plan.
   - If time has passed or anything was synced since the dry run, run `--dry-run` again first.
   - The user wants only some items back: use `--items`; a restore without it sets **everything** in the backup back.
   - `--trash-new` / `--erase-new` also remove what is **not** in the backup (to the trash, or for good, the trash too). Only when the user asks for exactly that; show the removal section of the `--dry-run` plan; for `--erase-new` say that it cannot be undone and that citations of erased items stop working, and get a yes to that. Prefer `--trash-new`.
3. **`prune` deletes backups without asking.** Run `zotero-backup prune --dry-run`, show the list, and run `prune` only after the user's yes. Deleting a backup frees only the files no other backup links to; never delete backup folders by hand instead.
4. Restore needs a **write key**: `--key NAME`, or the profile's `write_key` (shown by `zotero-backup config`; `key check NAME` shows whether it may write to the library). Without one, the user creates a key with write access at zotero.org and stores it with `zotero-backup key set NAME` in their own terminal. Use it only for `restore`; `backup`, `list`, `diff` and scheduled runs use the read-only key (the profile's `key`, which `key check` without NAME checks).
5. **Never run `stresstest`** yourself: it changes the libraries it runs on and asks per library on a terminal. If the user wants it, point them to the documentation's Stress test page; they run it in their own terminal on throw-away test libraries. To explain a report they share (`report.json`, `report.html`), **read [stresstest-report.md](stresstest-report.md)**.
6. A command failed or reports something unexpected: show the message; do not retry writes or try other keys on your own. A restore that is still different after the user synced and restored again: stop and point to the documentation's Restore page.

## Commands

| Task | Command |
|---|---|
| Version | `zotero-backup version` |
| Effective configuration (backup folder, libraries, keys, retention) | `zotero-backup config` |
| What a key may read and write | `zotero-backup key check [NAME]` |
| Back up (the profile's libraries) | `zotero-backup backup [--profile P] [--exclude files,fulltext]` |
| List backups (per library: date, version, items, files, missing, problems) | `zotero-backup list` |
| Find an item in the backups (offline): which backups have it, is it gone from the newest | `zotero-backup find TEXT [LIBRARY_FOLDER]` |
| Show an item from a backup; copy its files out | `zotero-backup show BACKUP KEY [--save DIR]` (DIR outside the backups) |
| What changed between two backups (offline) | `zotero-backup diff BACKUP1 BACKUP2` |
| What differs between a backup and the library today | `zotero-backup diff BACKUP [--items K1,K2]` |
| What a restore would do | `zotero-backup restore BACKUP --dry-run [--items K1,K2]` |
| Restore (after the user's yes) | `zotero-backup restore BACKUP --yes [--items K1,K2] [--key WRITEKEY]` |
| Restore exactly, removing what is not in the backup | `zotero-backup restore BACKUP --dry-run --trash-new` (or `--erase-new`), then the same with `--yes` |
| Which backups prune would delete | `zotero-backup prune --dry-run` |
| Is a newer version available | `zotero-backup upgrade --check` |
| Update zotero-backup (after the user's yes) | `zotero-backup upgrade stable --yes` (or `beta`) |

- `BACKUP` is a backup folder, or a library folder (then its newest backup). Find the backup of a given date with `list`; names are **UTC** times, so an evening backup may carry the next day's date: when the user's date is ambiguous, show the candidates and ask.
- Which libraries `backup` reads, where backups go (`to`) and how many prune keeps come from the profile (`zotero-backup config`): `library` is `all`, `user` or `group:<id>`. To back up only some libraries, the user adds a profile to the configuration file (documentation: Configuration) and you use `--profile`; there is no option to pick a library on the command line.
- To get one item back: `find` it, `show` it from the right backup (or `--save` its files when the user only needs a file), then `restore BACKUP --items KEY` after a dry run and a yes.
- Restore recreates items missing from the library with their old keys and takes items out of the trash; `diff` lists them under "missing in the library", which also gives their keys for `--items`.
- `--items` restores only these items (8-character keys), with their attachments, notes, annotations, parents and the collections they need; library settings are left alone.
- `--exclude` parts: `files`, `attachments`, `notes`, `annotations`, `fulltext`, `settings`, `trash`; `--exclude ""` for none (default: the profile's `exclude`). For `backup` they are left out of the backup; for `diff` / `restore` out of the plan (e.g. `--exclude files` when the storage is full).
- `--profile NAME` and `--config PATH` select another profile or configuration file. Progress goes to stderr: `--progress auto|line|log|quiet` (`log` for scheduled runs and log files), `--quiet` = only the summary and errors.
- Configuration (JSON, `profiles` by name; documentation: Configuration): `library`, `to` (backup folder), `key` (read key), `keys` (a key per library: `"user"` / `"group:<id>"` → keychain entry), `write_key` (for restore), `exclude`, `keep_daily` / `keep_weekly` / `keep_monthly` (prune), `progress`. Unknown fields are errors.
- `zotero-backup key remove [NAME]` deletes a keychain entry: only when the user asks.
- `upgrade` replaces the program itself (checked: signature, checksum, version). Run `upgrade --check` first, show what it offers, and update only after the user's yes; `stable` never goes back from a pre-release unless `--downgrade` is given. Built with `go install` or installed with Homebrew: it refuses and says how to update instead.

## Reading the output

- **Plan** (`diff`, `restore`): `missing in the library (restore recreates them with their keys)`, `different in the library (restore sets them back)` with the changed fields in `[…]`, `files to upload`, `settings to set back`, and `only in the library, not in the backup (restore keeps them)`; with `--trash-new` / `--erase-new` instead `not in the backup: moved to the trash` / `ERASED FOR GOOD` and `only in the library, already in the trash (kept)`. `The library matches the backup.` means nothing to do.
- **After a restore:** `checked: the library matches the backup` is success. `checked: still different from the backup:` plus an error means a Zotero app had unsynced changes or writes failed: the user syncs every Zotero app, then `--dry-run` and restore again (with a new yes).
- **Errors:** a failed command prints `zotero-backup: <message>` on stderr and exits with status 1. **Read [errors.md](errors.md)** for what each message means and the next step. `backup` warns `skipped <library>` for libraries it leaves out (e.g. the key cannot read them: `key check`).
- **A scheduled run:** `list` shows the newest backups with their missing files and problems. In the backup's `meta.json` (you may read it), `missing_files` are attachments whose file never reached the Zotero server (upload them from the computer that has them); `problems` should be empty. A leftover `<date>.partial` folder is an interrupted backup; the next `backup` continues it.
- A Zotero app showing its **conflict window** after a restore: the version chosen there wins; see the documentation's Restore page.

## Where to look

Documentation pages: Install, Keys, Configuration, Backups, Restore (what is set back and what is kept), Scheduled backups, Disclaimer. No warranty: the user is responsible for their data; keep independent copies of libraries that matter.
