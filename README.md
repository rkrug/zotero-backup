# zotero-backup

[![DOI](https://zenodo.org/badge/1447169768.svg)](https://doi.org/10.5281/zenodo.24090287)

Backup and restore of Zotero libraries (personal library and groups) through the Zotero web API, **keeping the item keys**, so restored items are the same items: citations in Word, LibreOffice and Google Docs keep working.

- **Complete backups.** Every backup is a folder with the whole library: items (with notes, annotations and the trash), collections, saved searches, library settings such as tag colours, the full-text index, and the attachment files.
- **Small and fast after the first run.** Files that did not change since the previous backup are hard links to it: not downloaded again, not stored twice.
- **Restore into the same library**, everything or selected items, with a preview first.
- One executable for Windows, macOS and Linux; nothing else to install.

**Platform status: tested on macOS.** zotero-backup is used and checked by hand on macOS, including the end-to-end [stress test](https://rkrug.github.io/zotero-backup/stresstest/) against Zotero desktop. The Windows and Linux versions are built for every release and pass all unit tests in CI on those systems, including the keychains, but have **not yet been tried on a real Windows or Linux system**: see [#15](https://github.com/rkrug/zotero-backup/issues/15) (Windows) and [#16](https://github.com/rkrug/zotero-backup/issues/16) (Linux). Reports are welcome.

The plan and the tests behind the design are in [Issue #1](https://github.com/rkrug/zotero-backup/issues/1).

> [!WARNING]
> **No warranty: you are responsible for your data.** zotero-backup comes as it is, without any warranty. Nobody guarantees that a backup is complete or that a restore brings a library back as it was. `restore` writes into your Zotero library and `prune` deletes backups: look at what they will do first (`diff`, `--dry-run`), and keep other, independent copies of libraries that matter. See the [disclaimer](https://rkrug.github.io/zotero-backup/disclaimer/).
>
> zotero-backup is an independent project, not made, endorsed or supported by Zotero or the Corporation for Digital Scholarship.

**Documentation: <https://rkrug.github.io/zotero-backup/>** (source in [docs/](docs/)).

## Install

Download the archive for your system from the [releases](https://github.com/rkrug/zotero-backup/releases), unpack it and put `zotero-backup` (`zotero-backup.exe` on Windows) somewhere on your `PATH`. On macOS, the first start is blocked ("Not Opened": the binary is not notarised by Apple): click **Done**, then allow it in System Settings → Privacy & Security → **Open Anyway**, or run `xattr -d com.apple.quarantine zotero-backup` ([Install](https://rkrug.github.io/zotero-backup/install/)).

Or build it with Go: `go install github.com/rkrug/zotero-backup@latest`.

From v0.3.0 on, `zotero-backup upgrade` updates it from the releases (signed and checked; [Updating](https://rkrug.github.io/zotero-backup/latest/install/#updating)).

## Quick start

1. **Create an API key** at <https://www.zotero.org/settings/keys>. For backups, a **read-only** key is enough: allow library access and notes for the personal library, and read access to the groups. **Store it** in the system keychain:

    ```bash
    # asks for the key (not shown), checks it with Zotero, stores it
    zotero-backup key set
    # the key's user, its libraries and what it may do in each
    zotero-backup key check
    ```

    On a server without a keychain, set `ZOTERO_API_KEY` instead ([Keys](https://rkrug.github.io/zotero-backup/keys/)).

2. **Back up** and look at what is there:

    ```bash
    # all libraries the key can read, into ~/zotero-backups
    zotero-backup backup
    zotero-backup list
    ```

3. **Restore** needs a second key with **write** access, used only for restores:

    ```bash
    zotero-backup key set zotero-write
    # what differs from the library today
    zotero-backup diff ~/zotero-backups/group-<id>-<name>
    # what restore would do (without --dry-run: shows the plan, asks, restores)
    zotero-backup restore ~/zotero-backups/group-<id>-<name> --key zotero-write --dry-run
    ```

    With `"write_key": "zotero-write"` in the [configuration](https://rkrug.github.io/zotero-backup/configuration/), `restore` uses it without `--key`.

4. **Clean up** old backups (keeps the newest of each of the last 7 days, 5 weeks and 12 months):

    ```bash
    zotero-backup prune --dry-run
    ```

## Development

```bash
go vet ./... && go test -race ./...
go build -o zotero-backup .
.github/build.sh v0.0.0-test     # release archives for all systems into dist/
```

Releases are built by GitHub Actions for each tag `v*`. See `CLAUDE.md` for the structure.

**Update signing key** (for `zotero-backup upgrade`): the release workflow signs `SHA256SUMS` with the Ed25519 key in the secret `ZB_UPDATE_SIGNING_KEY` (`SHA256SUMS.sig`); `updatePublicKey` in `upgrade.go` is its public key. To create a key pair (once; a new key needs a release with the new public key, and older binaries then only point to the release page):

```bash
go run tools/sign.go keygen zb-update-key        # writes the private key (readable only by you), prints the public key
gh secret set ZB_UPDATE_SIGNING_KEY < zb-update-key
```

Keep `zb-update-key` somewhere safe (not in the repository).

The documentation site is built with [MkDocs](https://www.mkdocs.org/) (Material theme) from `docs/` and published to the `gh-pages` branch by `.github/workflows/docs.yml` with [mike](https://github.com/jimporter/mike), one version per minor release (`0.2`, with `latest` as the default; a pre-release as `0.3 (pre-release)` until its release) plus `dev` (unreleased):

```bash
pip install mkdocs-material==9.6.14
mkdocs serve          # preview at http://127.0.0.1:8000
```

## AI assistants

[skills/zotero-backup/SKILL.md](skills/zotero-backup/SKILL.md) is an Agent Skill that tells an assistant (e.g. Claude Code) how to use zotero-backup safely: keys only typed by you, a preview and your yes before every `restore` and `prune`. For Claude Code, copy the folder `skills/zotero-backup` into `~/.claude/skills/`.

## Contributing

Reports, tests on Windows and Linux, and pull requests (into `dev`) are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). Security problems: report them privately, see [SECURITY.md](SECURITY.md). What changed per version: [CHANGELOG.md](CHANGELOG.md).

## Citing

zotero-backup is archived on Zenodo: [10.5281/zenodo.24090287](https://doi.org/10.5281/zenodo.24090287) cites all versions; each release has its own DOI there. GitHub's "Cite this repository" (from [CITATION.cff](CITATION.cff)) gives the reference.

## Licence

MIT, see [LICENSE](LICENSE). No warranty: see the [disclaimer](https://rkrug.github.io/zotero-backup/disclaimer/).
