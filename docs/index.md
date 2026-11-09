# zotero-backup

Backup and restore of Zotero libraries (personal library and groups) through the Zotero web API, **keeping the item keys**, so restored items are the same items: citations in Word, LibreOffice and Google Docs keep working.

- **Complete backups.** Every backup is a folder with the whole library: items (with notes, annotations and the trash), collections, saved searches, library settings such as tag colours, the full-text index, and the attachment files.
- **Small and fast after the first run.** Files that did not change since the previous backup are hard links to it: not downloaded again, not stored twice.
- **Restore into the same library**, everything or selected items, with a preview first.
- One executable for Windows, macOS and Linux; nothing else to install.

The plan and the tests behind the design are in [Issue #1](https://github.com/rkrug/zotero-backup/issues/1).

!!! warning "No warranty: you are responsible for your data"
    zotero-backup comes **as it is, without any warranty**. Nobody guarantees that a backup is complete or that a restore brings a library back as it was. `restore` writes into your Zotero library and `prune` deletes backups: look at what they will do first (`diff`, `--dry-run`), and keep other, independent copies of libraries that matter. Read the [disclaimer](disclaimer.md).

## Quick start

```bash
zotero-backup backup                # all libraries the key can read, into ~/zotero-backups
zotero-backup list
zotero-backup diff ~/zotero-backups/group-2333076-IPBES_LDR
zotero-backup restore ~/zotero-backups/group-2333076-IPBES_LDR --dry-run
zotero-backup prune --dry-run
```

Next: [install](install.md) it, store an [API key](keys.md), and read about [backups](backups.md) and [restore](restore.md).

## Not covered

- Linked files (only the link is on the Zotero server).
- What exists only in the Zotero app: feeds, plugins, styles, preferences. Copy the Zotero data folder for that.
- Restoring into a *different* library, e.g. a new group after a group was deleted: citations link to the old library; planned in [Issue #11](https://github.com/rkrug/zotero-backup/issues/11).
