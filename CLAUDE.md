# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`zotero-backup`: back up and restore Zotero libraries (personal and group) through the Zotero web API, **preserving item keys** across backup/restore. Go, standard library only; a CLI first, a local web interface (`serve`) later. The plan, the revised first steps and all API test results are in Issue #1 (read its comments: they override parts of the description).

## Commands

```bash
go build -o zotero-backup .     # build
go vet ./...                    # lint (CI also checks gofmt -l .)
go test ./...                   # all tests
go test -run TestKeyCheck ./... # one test
```

Run locally without a config file, using a keychain entry (macOS) or the environment:

```bash
ZOTERO_API_KEY=... ./zotero-backup key check
./zotero-backup key check --config cfg.json --profile test
./zotero-backup config          # prints the effective config incl. all defaults
```

## Structure

One `main` package, kept flat: `main.go` (command dispatch, key lookup, `key check`), `config.go` (JSON profiles), `zotero.go` (API client). Tests use `httptest` fake servers (`fakeZotero` in `main_test.go`); no test talks to the real API.

- **Config:** JSON, `profiles` by name. Every profile starts from `defaultProfile()`; fields missing in the file keep the default. Unknown fields and invalid values are errors. Default path: `os.UserConfigDir()/zotero-backup/config.json`; a missing file means defaults only.
- **API key:** `ZOTERO_API_KEY`, else the keychain entry named by the profile's `key` (macOS `security` tool for now). Never print, log or store the key, and never take it as a command-line argument.
- **Library spec:** `all`, `user` or `group:<id>` (`parseLibrary`).
- **API client:** all reads go through `Client.get` (retries network errors, 429 and 5xx; honours `Retry-After` and `Backoff`; `Sleep` is injectable for tests). `getAll[T]` reads every page and fails on a `Total-Results` mismatch or with `errLibraryChanged` if the library version moves between pages.

## Zotero API facts the design depends on (tested, see Issue #1)

- Backups store the API's own JSON `data`. Restore sends it back with `version: 0` and without `dateModified` (attachments also without `md5`/`mtime`); the old key is reused.
- Restore order: collections (parents first) → searches → top-level items → attachments/notes → files → annotations → settings → full text. Items or collections now in the trash need `"deleted": false` sent explicitly.
- Write preconditions per object (`version` in the body), not the library-wide header. Each object in a batch gets its own version.
- Deleting a collection removes it from items' `collections` without bumping the items' versions; incremental backup must derive this from `/deleted`.
- Long runs need retries on network errors (not only 429/503 and `Backoff`) and must be resumable.

## Git

Work on `dev`; `main` is the base branch for pull requests.
