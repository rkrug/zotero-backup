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

**Before a restore, sync every Zotero app that works on the library.** A Zotero app with unsynced changes shows a conflict dialog on its next sync, and whatever is chosen there wins over the restore. If that happened, run `restore` again; or, on that computer, use Zotero → Settings → Sync → Reset → **Restore from Online Library** (not "Replace Online Library", which goes the other way).

Restoring files counts against the library owner's Zotero storage.
