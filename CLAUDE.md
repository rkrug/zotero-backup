# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`zotero-backup`: back up and restore Zotero libraries (personal and group) through the Zotero web API, **preserving item keys** across backup/restore. Go, standard library only; a CLI first, a local web interface (`serve`) later. The plan, the revised first steps and all API test results are in Issue #1 (read its comments: they override parts of the description).

## Commands

```bash
go build -o zotero-backup .     # build
go vet ./...                    # lint (CI also checks gofmt -l .)
go test -race ./...             # all tests (CI: Linux, macOS, Windows)
go test -run TestKeyCheck ./... # one test
.github/build.sh v0.0.0-test    # release archives for all systems into dist/ (CI runs it; tags v* release it)
./zotero-backup stresstest --key zb-test-write   # end-to-end against the real API + running Zotero desktop; CHANGES every library the key may write to, after a [N/y] per library (docs/stresstest.md)
```

Run locally without a config file, using a keychain entry (macOS) or the environment:

```bash
ZOTERO_API_KEY=... ./zotero-backup key check
./zotero-backup key check --config cfg.json --profile test
./zotero-backup config          # prints the effective config incl. all defaults
./zotero-backup backup --profile test [--exclude files,fulltext]
./zotero-backup list --profile test
./zotero-backup prune --profile test --dry-run
./zotero-backup diff ~/zotero-backups/group-6719916-Safe_Edit_Test --profile test
./zotero-backup restore ~/zotero-backups/group-6719916-Safe_Edit_Test --profile test --dry-run
```

## Structure

One `main` package, kept flat: `main.go` (command dispatch, key lookup, `key check`, `list`), `config.go` (JSON profiles), `zotero.go` (API client), `backup.go` (backup), `prune.go` (three-generation retention), `restore.go` (diff, restore), `zotero_write.go` (API writes, file upload), `progress.go` (progress on stderr). Writes are tested against `fakeLibrary` (`backup_test.go` + `fake_write_test.go`), which applies preconditions, write tokens and the three-step upload like the API. Tests use `httptest` fake servers (`fakeZotero` in `main_test.go`, `fakeLibrary` in `backup_test.go`); no test talks to the real API. The end-to-end test is the hidden command `stresstest` (`stresstest*.go`, docs/stresstest.md): real API plus a running Zotero through the Debug Bridge plugin; only libraries the write key may write to (all, or those named with `--library`, and then the key may write to nothing else: `checkKeyScope`), a [N/y] question per library by name on a terminal, and only its own `zb-e2e` objects. Its unit tests cover the dialog, refusals and reports, never a real run.

- **Backups:** `<to>/<group-ID-Name|user-ID-username>/<YYYY-MM-DDTHHMMSSZ>/` (name made safe, spaces as `_`; the folder is found by ID and renamed when the name changes), each **complete**: `items.json`, `collections.json`, `searches.json`, `settings.json`, `fulltext.json` (API `data` as is), `files.json`, `files/<attachment key>/<safe name>`, `meta.json` (version, counts, missing files, problems). No incremental metadata: every run reads everything. A file whose md5 matches one in the previous backup (or earlier in this run) is a **hard link** (copy where links fail), so unchanged files are neither downloaded nor stored twice. Files are never modified in place. A run writes `<date>.partial` and renames it when complete; a leftover `.partial` is resumed. The metadata is re-read (up to 3 times) if the library version differs between the reads.
- **Restore:** plan = missing / changed objects (fields compared without server-managed ones), files whose md5 differs, settings; written in the tested order with per-object versions; `deleted` always sent explicitly; fields absent from the backup are cleared; ends by reading the library again and comparing. Output: plan and summary on stdout, progress on stderr.
- **Prune:** `keep_daily` / `keep_weekly` / `keep_monthly` keep the newest backup of each of the last N days / ISO weeks / months; the newest backup always stays. Deleting a folder frees only files no other backup links to.

- **Config:** JSON, `profiles` by name. Every profile starts from `defaultProfile()`; fields missing in the file keep the default. Unknown fields and invalid values are errors. Default path: `os.UserConfigDir()/zotero-backup/config.json`; a missing file means defaults only.
- **API key:** `ZOTERO_API_KEY`, else the keychain entry named by the profile's `key`: `keychain_darwin.go` (`security`; the key goes in through `security -i` on stdin), `keychain_windows.go` (Credential Manager via advapi32, no dependency), `keychain_linux.go` (`secret-tool`, key on stdin). `key set` reads the key without echo (`echo_*.go`) and checks it before storing. Never print, log or store the key elsewhere, and never pass it as a command-line argument. Per library, `keys` in a profile names other entries (`Profile.keyName`); `clientsFor` gives one client per key name, `libraryJobs` pairs each library with its client (for `all`, the default key lists libraries; `keys` entries are read with their own key), and `diff` / `restore` use the key for the backup's library; `restore` uses `write_key` instead when it is set (`Profile.forRestore`). `TestOSKeychain` touches the real keychain only with `ZB_KEYCHAIN_TEST=1` (CI sets it; Linux CI runs GNOME Keyring under `dbus-run-session`).
- **Library spec:** `all`, `user` or `group:<id>` (`parseLibrary`).
- **API client:** all reads go through `Client.get` (retries network errors, 429 and 5xx; honours `Retry-After` and `Backoff`; `Sleep` is injectable for tests). `getAll[T]` reads every page and fails on a `Total-Results` mismatch or with `errLibraryChanged` if the library version moves between pages.

## Zotero API facts the design depends on (tested, see Issue #1)

- Backups store the API's own JSON `data`. Restore sends it back with `version: 0` and without `dateModified` (attachments also without `md5`/`mtime`); the old key is reused.
- Restore order: collections (parents first) → searches → top-level items → attachments/notes → files → annotations → settings → full text. Items or collections now in the trash need `"deleted": false` sent explicitly.
- Write preconditions per object (`version` in the body), not the library-wide header. Each object in a batch gets its own version.
- Long runs need retries on network errors (not only 429/503 and `Backoff`) and must be resumable.
- Attachments without `md5` usually have no file on the server (404); they are listed in `meta.json` as missing, not skipped silently.
- Zotero desktop uploads text/* attachments (snapshots too) and attachments with several files as a ZIP (`zipMD5`, `zipFilename` in the upload authorisation); the API then serves that ZIP, while the item's `md5` is the file's inside. Backups keep it as `<name>.zip` with `"zip": true` in `files.json`; restore uploads it the same way; `loadBackup` recognises such ZIPs in older backups (kept under the file's own name with the ZIP's md5).

## Git

Work on `dev`; `main` is the base branch for pull requests.
