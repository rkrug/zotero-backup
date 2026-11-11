# zotero-backup: errors and what to do

Errors print `zotero-backup: <message>` on stderr; the exit status is 1. Show the user the message, explain it with this table, and suggest the next step. Never fix a key problem by handling the key yourself (SKILL.md, rule 1), and never retry a restore without a new `--dry-run` and yes.

## Keys and keychain

| Message (start) | Meaning | Next step |
|---|---|---|
| `no API key: ZOTERO_API_KEY is not set and there is no keychain entry "NAME"` | no key stored under that name | the user runs `zotero-backup key set NAME` in their own terminal; check the profile's `key` / `keys` / `write_key` names with `zotero-backup config` |
| `no API key: … the keychain could not be read` | the keychain is locked or not reachable (e.g. a scheduled run while logged out, SSH session) | unlock / log in, or for scheduled runs see the documentation's Scheduled backups page (key file) |
| `no keychain: install secret-tool …` / `no keychain support on this system` | Linux without Secret Service, or another system | install `secret-tool` (libsecret-tools) with a running keyring, or the user sets `ZOTERO_API_KEY` |
| `Zotero does not accept this key` | key deleted, mistyped or revoked at zotero.org | the user creates a new key at zotero.org/settings/keys and stores it with `key set` |
| `the key has no access to …` / `the key has no access to the personal library` / `… the key's user is not a member of this group` | the key may not read that library | widen the key's access at zotero.org (or use the right key name); `key check NAME` shows what it may read |
| `the API key may not write to …; restore needs a key with write access` | restore with a read-only key | use the write key: `--key NAME` or `write_key` in the profile; `key check NAME` shows write access |
| `key check failed for …` | at least one key in the list failed | the lines above it name the key and the reason |
| `storing in the keychain failed` | `key set` could not write the entry | keychain locked or denied; the user unlocks it and runs `key set` again |

## Configuration and command line

| Message (start) | Meaning | Next step |
|---|---|---|
| `no profile "NAME" in PATH` | `--profile` names a profile the file does not have | `zotero-backup config` lists the profiles |
| `config: …` (unknown field, invalid value) | the configuration file has an error; unknown fields are errors | fix the named field (documentation: Configuration) |
| `library "…": use …` / `group ID must be a positive number` | wrong library spec | `all`, `user` or `group:<id>` |
| `exclude: unknown value` / `progress: … is not one of …` / `keep_… must not be negative` / `progress_interval must be at least 1` | invalid option value | the message lists the allowed values |
| `unknown command` / `no command given` / `… needs one backup folder` | wrong command line | `zotero-backup --help` |
| `… is neither a backup nor a library folder with backups` | wrong `BACKUP` path | `zotero-backup list`, and the backup folder (`to`) from `config` |

## Backup

| Message (start) | Meaning | Next step |
|---|---|---|
| `a backup of … is already running (PID … on …, since …): skipped` | another run (by hand or from the schedule) works on that library | nothing to do: that run makes the backup; `zotero-backup status` shows it, `stop` stops it after the user's yes |
| `… it is from another computer: if no backup runs there, remove …` | a lock in a backup folder shared with another computer | the user checks that computer; only then remove the named `.lock` file |
| `taking over the lock of … left by a run that no longer runs` (warning) | an earlier run crashed or was killed | nothing: the new run continues its `.partial` |
| `stopped (an unfinished backup is continued by the next run)` | the run got Ctrl-C or `zotero-backup stop` | run `backup` again, or wait for the schedule |
| `PROBLEMS <backup>: …` from `verify` (md5 / size / missing file / JSON changed or unreadable / counts) | that backup is damaged (disk, copy, someone edited it) | do not restore from it; use another backup (`verify all` shows which are OK); the next `backup` makes a complete one; the user decides whether to delete the damaged one |
| `N of M libraries failed` | some libraries failed, the others were backed up | the lines above name each library and its error |
| `library changed while reading; read it again` | the library changed during the backup (3 attempts), e.g. a Zotero app syncing a lot | run `backup` again later; a leftover `.partial` is continued |
| `GET …: got N objects, Total-Results says M` / `no Total-Results header` | the server's answer was incomplete | run `backup` again; if it repeats, report it (bug report) |
| `giving up after N attempts` | network or server errors (429 and 5xx are retried with waits) | check the connection, run again later; an interrupted backup is continued |
| `GET …: HTTP 403` | the key may not read this | see Keys above |
| `both A and B exist for ID; merge or remove one` | two folders for the same library (e.g. after a rename) | the user decides which to keep; never merge or delete backup folders yourself |

`missing_files` / `problems` in `meta.json` are not errors of the run: see SKILL.md, Reading the output.

## Restore

| Message (start) | Meaning | Next step |
|---|---|---|
| `not on a terminal: use --dry-run to look, --yes to restore` | restore without `--yes` outside a terminal | only after a yes to the plan just shown (SKILL.md, rule 2) |
| `not restored` | the user answered no | nothing changed |
| `FAILED <kind> <key>: …` lines, then `restore incomplete: N failures, … still differ (Zotero clients with unsynced changes? …)` | some writes failed (often HTTP 412: the object changed meanwhile, e.g. a Zotero app synced) | the user syncs every Zotero app; then `--dry-run`, show the plan, new yes, restore again |
| `… (not retried: the server may have applied it)` | a network error during a write | run `--dry-run`: it shows what is still to do |
| `the file on the server changed meanwhile` | another client replaced the file during the restore | sync, `--dry-run`, restore again after a yes |
| `upload to storage: HTTP 413` | the owner's Zotero storage is full | free storage or upgrade it at zotero.org; or restore everything but the files with `--exclude files` (new `--dry-run` and yes) |
| `upload to storage: …` (other) | the file upload failed | run `--dry-run` and restore again after a yes |
| `no Last-Modified-Version in the answer; run restore again to take them out of the trash` | subcollections could not be touched | `--dry-run`, restore again after a yes |
| `--trash-new / --erase-new act on the whole library: not with --items` / `--trash-new or --erase-new, not both` | wrong combination | one of them, without `--items` |
| `FAILED items K: not erased, K2 below it is in the backup` | `--erase-new` would have erased an object of the backup with its parent (a write before failed) | sync, `--dry-run`, restore again after a yes |
| `FAILED trash … ` / `FAILED erasing …` | removing failed (e.g. the object changed meanwhile) | sync, `--dry-run`, restore again after a yes |
| `restored, but checking the result failed` | the restore ran; reading the library afterwards failed | `zotero-backup diff BACKUP` shows the state |

## Upgrade

| Message (start) | Meaning | Next step |
|---|---|---|
| `an update is available: zotero-backup upgrade …` | `--check` found a newer release (exit status 1 on purpose) | show it; update only after the user's yes |
| `… is not a release build` / `installed with Homebrew` | built with `go install`, or a Homebrew install | update the way it was installed (the message says how) |
| `… nothing was replaced. Download it from …` (signature not valid, checksum, unexpected address, the new file reports another version) | a check failed: the download is not trusted | do not work around it; the user downloads the release by hand from the page, and reports it (bug report) |
| `this build cannot verify updates` | an older build without the update key | download the release by hand |
| `cannot write to …: run it again with sudo` | the program sits in a folder the user cannot write to | the user runs the shown command themselves |
| `not on a terminal: update with zotero-backup upgrade … --yes` / `name the channel` | it needs a yes or a channel | ask the user, then `upgrade stable --yes` or `beta --yes` |

## No message

| Symptom | Meaning | Next step |
|---|---|---|
| a scheduled run on macOS stays `running` (`schedule show`), the log stops after its first lines | macOS waits for the user to allow access to the backup folder (another volume, Documents, Desktop, Downloads), in a dialog on the screen | the user clicks Allow on that computer's screen (Screen Sharing for a server), or gives the program Full Disk Access in System Settings → Privacy & Security; check again after an `upgrade` |

For anything else: show the message and the command, and point to a bug report (no API keys, no private library content): <https://github.com/rkrug/zotero-backup/issues/new?template=bug_report.md>.
