# Restore

!!! warning "Restore changes your library"
    Run `diff` and `restore --dry-run` first, and sync every Zotero app before restoring. You are responsible for the result: see the [disclaimer](disclaimer.md).

```bash
zotero-backup diff BACKUP                     # what differs from the library today
zotero-backup diff BACKUP1 BACKUP2            # what changed between two backups (offline)
zotero-backup restore BACKUP --dry-run        # what restore would do
zotero-backup restore BACKUP                  # shows the plan, asks, restores, checks
zotero-backup restore BACKUP --items ABCD1234,EFGH5678
zotero-backup restore BACKUP --trash-new      # also move what is not in the backup to the trash
zotero-backup restore BACKUP --erase-new      # also erase what is not in the backup, for good
```

`BACKUP` is a backup folder, or a library folder for its newest backup. Restore writes into the **same library** the backup came from: missing items, collections and searches are recreated with their old keys, changed ones set back, items taken out of (or put back into) the trash, files uploaded (or attached again if Zotero still has them), tag colours set back. Items added since the backup are kept. `--items` restores only these items, with their children (attachments, notes, annotations), their parents and the collections they need. At the end the library is read again and compared with the backup.

## What restore changes and what it keeps

Restore **never deletes** anything that is not in the backup, unless asked to with `--trash-new` or `--erase-new` ([below](#removing-what-is-not-in-the-backup)). Everything that **is** in the backup is set back to its state at the time of the backup.

| Set back to the backup | Kept as it is |
|---|---|
| Items, collections and saved searches missing from the library: recreated with their old keys | Items, notes, annotations, attachments, collections and saved searches **created after the backup** |
| Objects changed since the backup: all fields, tags, collection memberships, note text; a field that was empty in the backup is cleared again | The collection memberships of those new items (a new item in an old collection stays there) |
| Trash state: items and collections put back into or taken out of the trash as they were; subcollections of a collection taken out of the trash are written once more, so that Zotero desktop takes them out of its trash too (Zotero desktop trashes them with their parent only locally) | |
| Files whose content differs from the backup: uploaded again | |
| Library settings, e.g. tag colours, **as a whole**: a tag colour assigned after the backup is removed | |

So changes made since the backup to anything that was in the backup are lost. The plan lists every object that will change before you confirm, and `diff` or `--dry-run` show it beforehand; objects that are only in the library are listed as "only in the library, not in the backup (restore keeps them)".

## Removing what is not in the backup

By default restore keeps everything created after the backup. To get the library back **exactly** as it was:

- **`--trash-new`** also moves everything that is not in the backup to the Zotero **trash**: new items (their attachments, notes and annotations go with them), new notes, attachments and annotations under items that are in the backup, new collections and subcollections, new saved searches. From the trash they can be brought back, and citations of them in Word or Google Docs still work until the trash is emptied. What is already in the trash stays there.
- **`--erase-new`** **erases** all of that for good, including what is not in the backup but already in the trash. Erased items cannot be brought back (only from another backup), their files are gone from the Zotero storage, and citations of them in documents no longer resolve.

Both are given on the command line each time (they cannot be set in the configuration), cannot be combined with `--items`, and show in the plan as their own section ("not in the backup: moved to the trash" or "ERASED FOR GOOD") with a count, and in the question. Run `--dry-run` with them first.

- **What a backup left out is never removed:** a backup made with `--exclude notes` has no notes, so the library's notes do not count as "not in the backup" (the same for attachments, annotations and the trash).
- Removing happens after everything in the backup was written back, so an object that had been moved under a new item or collection is back in place before that is removed. Erasing never takes an object that is in the backup with it.
- As for any restore: **sync every Zotero app first**. An app with unsynced new items uploads them again afterwards.

## Selected items

With `--items`, only the chosen items change (with their children, parents and the collections they need); library settings are left alone.

**Before a restore, sync every Zotero app that works on the library.** If the restore changed an object that a Zotero app has unsynced changes to, that app shows the conflict window on its next sync (even when the two changes are in different fields), and whatever is chosen there wins over the restore. Unsynced changes to objects the restore did not change are uploaded as usual and stay. If that happened, run `restore` again; or, on that computer, use Zotero → Settings → Sync → Reset → **Restore from Online Library** (not "Replace Online Library", which goes the other way).

Restoring files counts against the library owner's Zotero storage.
