# zotero-backup

Backup and restore of Zotero libraries (personal library and groups) through the Zotero web API, **keeping the item keys**, so restored items are the same items: citations in Word, LibreOffice and Google Docs keep working.

- **Complete backups.** Every backup is a folder with the whole library: items (with notes, annotations and the trash), collections, saved searches, library settings such as tag colours, the full-text index, and the attachment files.
- **Small and fast after the first run.** Files that did not change since the previous backup are hard links to it: not downloaded again, not stored twice.
- **Restore into the same library**, everything or selected items, with a preview first.
- **Look into backups without restoring** (`find`, `show`, `diff` of two backups), **schedule** the daily backup with one command, and **update** zotero-backup itself (`upgrade`, signed).
- One executable for Windows, macOS and Linux; nothing else to install.

!!! note "Platform status: tested on macOS"
    zotero-backup is used and checked by hand on macOS, including the end-to-end [stress test](stresstest.md) against Zotero desktop. The Windows and Linux versions are built for every release and pass all unit tests in CI on those systems, including the keychains, but have **not yet been tried on a real Windows or Linux system**: see [#15](https://github.com/rkrug/zotero-backup/issues/15) (Windows) and [#16](https://github.com/rkrug/zotero-backup/issues/16) (Linux). Reports are welcome.

The plan and the tests behind the design are in [Issue #1](https://github.com/rkrug/zotero-backup/issues/1).

**Citing:** zotero-backup is archived on Zenodo, [doi:10.5281/zenodo.24090287](https://doi.org/10.5281/zenodo.24090287) (all versions; each release has its own DOI there).

!!! warning "No warranty: you are responsible for your data"
    zotero-backup comes **as it is, without any warranty**. Nobody guarantees that a backup is complete or that a restore brings a library back as it was. `restore` writes into your Zotero library and `prune` deletes backups: look at what they will do first (`diff`, `--dry-run`), and keep other, independent copies of libraries that matter. Read the [disclaimer](disclaimer.md).

## Quick start

1. **Create an API key** at <https://www.zotero.org/settings/keys>. For backups, a **read-only** key is enough: allow library access and notes for the personal library, and read access to the groups. For restores, create a second, **read/write** key (step 3): store both ([two keys](keys.md#two-keys-one-to-back-up-one-to-restore)). **Store it** in the system keychain:

    ```bash
    # asks for the key (not shown), checks it with Zotero, stores it
    zotero-backup key set
    # the key's user, its libraries and what it may do in each
    zotero-backup key check
    ```

    On a server without a keychain, set `ZOTERO_API_KEY` instead ([Keys](keys.md)).

2. **Back up** and look at what is there:

    ```bash
    # all libraries the key can read, into ~/zotero-backups (create it once: it is never created for you)
    mkdir -p ~/zotero-backups
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

    With `"write_key": "zotero-write"` in the [configuration](configuration.md), `restore` uses it without `--key`.

4. **Clean up** old backups (keeps the newest of each of the last 7 days, 5 weeks and 12 months):

    ```bash
    zotero-backup prune --dry-run
    ```

First [install](install.md) it; more about [keys](keys.md), [backups](backups.md) and [restore](restore.md).

**AI assistants:** [skills/zotero-backup/SKILL.md](https://github.com/rkrug/zotero-backup/blob/main/skills/zotero-backup/SKILL.md) is an Agent Skill that tells an assistant (e.g. Claude Code) how to use zotero-backup safely: keys only typed by you, a preview and your yes before every `restore` and `prune`. For Claude Code, copy the folder `skills/zotero-backup` into `~/.claude/skills/`.

**Contributing:** reports, tests on Windows and Linux, and pull requests are welcome, see [CONTRIBUTING.md](https://github.com/rkrug/zotero-backup/blob/main/CONTRIBUTING.md); security problems are reported privately, see [SECURITY.md](https://github.com/rkrug/zotero-backup/blob/main/SECURITY.md). What changed per version: [CHANGELOG.md](https://github.com/rkrug/zotero-backup/blob/main/CHANGELOG.md).

## Not covered

- Linked files (only the link is on the Zotero server).
- What exists only in the Zotero app: feeds, plugins, styles, preferences. Copy the Zotero data folder for that.
- Restoring into a *different* library, e.g. a new group after a group was deleted: citations link to the old library; planned in [Issue #11](https://github.com/rkrug/zotero-backup/issues/11).
