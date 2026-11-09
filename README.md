# zotero-backup

Backup and restore of Zotero libraries (personal library and groups) through the Zotero web API, **keeping the item keys**, so restored items are the same items: citations in Word, LibreOffice and Google Docs keep working.

- **Complete backups.** Every backup is a folder with the whole library: items (with notes, annotations and the trash), collections, saved searches, library settings such as tag colours, the full-text index, and the attachment files.
- **Small and fast after the first run.** Files that did not change since the previous backup are hard links to it: not downloaded again, not stored twice.
- **Restore into the same library**, everything or selected items, with a preview first.
- One executable for Windows, macOS and Linux; nothing else to install.

The plan and the tests behind the design are in [Issue #1](https://github.com/rkrug/zotero-backup/issues/1).

> [!WARNING]
> **No warranty: you are responsible for your data.** zotero-backup comes as it is, without any warranty. Nobody guarantees that a backup is complete or that a restore brings a library back as it was. `restore` writes into your Zotero library and `prune` deletes backups: look at what they will do first (`diff`, `--dry-run`), and keep other, independent copies of libraries that matter. See the [disclaimer](https://rkrug.github.io/zotero-backup/disclaimer/).
>
> zotero-backup is an independent project, not made, endorsed or supported by Zotero or the Corporation for Digital Scholarship.

**Documentation: <https://rkrug.github.io/zotero-backup/>** (source in [docs/](docs/)).

## Install

Download the archive for your system from the [releases](https://github.com/rkrug/zotero-backup/releases), unpack it and put `zotero-backup` (`zotero-backup.exe` on Windows) somewhere on your `PATH`. On macOS, the first start of a downloaded binary may need `xattr -d com.apple.quarantine zotero-backup`.

Or build it with Go: `go install github.com/rkrug/zotero-backup@latest`.

## Quick start

```bash
zotero-backup backup                # all libraries the key can read, into ~/zotero-backups
zotero-backup list
zotero-backup diff ~/zotero-backups/group-2333076-IPBES_LDR
zotero-backup restore ~/zotero-backups/group-2333076-IPBES_LDR --dry-run
zotero-backup prune --dry-run
```

## Development

```bash
go vet ./... && go test -race ./...
go build -o zotero-backup .
.github/build.sh v0.0.0-test     # release archives for all systems into dist/
```

Releases are built by GitHub Actions for each tag `v*`. See `CLAUDE.md` for the structure.

The documentation site is built with [MkDocs](https://www.mkdocs.org/) (Material theme) from `docs/` and published to the `gh-pages` branch by `.github/workflows/docs.yml` on every push to `main` and every normal release tag (not from `dev` or pre-releases):

```bash
pip install mkdocs-material==9.6.14
mkdocs serve          # preview at http://127.0.0.1:8000
```

## Licence

MIT, see [LICENSE](LICENSE). No warranty: see the [disclaimer](https://rkrug.github.io/zotero-backup/disclaimer/).
