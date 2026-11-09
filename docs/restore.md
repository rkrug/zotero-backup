# Restore

!!! warning "Restore changes your library"
    Run `diff` and `restore --dry-run` first, and sync every Zotero app before restoring. You are responsible for the result: see the [disclaimer](disclaimer.md).

```bash
zotero-backup diff BACKUP                     # what differs from the library today
zotero-backup restore BACKUP --dry-run        # what restore would do
zotero-backup restore BACKUP                  # shows the plan, asks, restores, checks
zotero-backup restore BACKUP --items ABCD1234,EFGH5678
```

`BACKUP` is a backup folder, or a library folder for its newest backup. Restore writes into the **same library** the backup came from: missing items, collections and searches are recreated with their old keys, changed ones set back, items taken out of (or put back into) the trash, files uploaded (or attached again if Zotero still has them), tag colours set back. Items added since the backup are kept. `--items` restores only these items, with their children (attachments, notes, annotations), their parents and the collections they need. At the end the library is read again and compared with the backup.

## What restore changes and what it keeps

Restore **never deletes** anything that is not in the backup. Everything that **is** in the backup is set back to its state at the time of the backup.

| Set back to the backup | Kept as it is |
|---|---|
| Items, collections and saved searches missing from the library: recreated with their old keys | Items, notes, annotations, attachments, collections and saved searches **created after the backup** |
| Objects changed since the backup: all fields, tags, collection memberships, note text; a field that was empty in the backup is cleared again | The collection memberships of those new items (a new item in an old collection stays there) |
| Trash state: items and collections put back into or taken out of the trash as they were; subcollections of a collection taken out of the trash are written once more, so that Zotero desktop takes them out of its trash too (Zotero desktop trashes them with their parent only locally) | |
| Files whose content differs from the backup: uploaded again | |
| Library settings, e.g. tag colours, **as a whole**: a tag colour assigned after the backup is removed | |

So changes made since the backup to anything that was in the backup are lost. The plan lists every object that will change before you confirm, and `diff` or `--dry-run` show it beforehand; objects that are only in the library are listed as "only in the library, not in the backup (restore keeps them)".

With `--items`, only the chosen items change (with their children, parents and the collections they need); library settings are left alone.

**Before a restore, sync every Zotero app that works on the library.** A Zotero app with unsynced changes shows a conflict dialog on its next sync, and whatever is chosen there wins over the restore. If that happened, run `restore` again; or, on that computer, use Zotero → Settings → Sync → Reset → **Restore from Online Library** (not "Replace Online Library", which goes the other way).

Restoring files counts against the library owner's Zotero storage.
