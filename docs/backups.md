# Backups

```
~/zotero-backups/
  group-2333076-IPBES_LDR/
    2026-11-05T190523Z/
      meta.json          library version, counts, files missing on the server, problems
      items.json  collections.json  searches.json  settings.json  fulltext.json
      files.json         attachment key → file name, md5, size
      files/<attachment key>/<file name>       (or <file name>.zip, see below)
    2026-11-06T060000Z/  …complete as well
  user-12022430-Rainer_M_Krug/
```

- Folder names are `group-<id>-<name>` and `user-<id>-<username>` (spaces as `_`). A renamed group keeps its folder: it is found by ID and renamed.
- Backup names are the time in **UTC** (`YYYY-MM-DDTHHMMSSZ`, no colons so they work on Windows), independent of the computer's time zone and of summer time; they sort by time.
- Text files and web-page snapshots are kept as `<file name>.zip`: Zotero desktop uploads them zipped (a snapshot with its images and styles) and the server hands out that ZIP. Unzip it to open the file; restore uploads the ZIP the way Zotero does.
- A file that did not change since the previous backup (same md5) is a **hard link**: deleting a backup frees only the files no other backup links to. On drives without hard links (FAT/exFAT, some network shares) files are copied instead. Never edit files inside a backup: a hard-linked file is the same file in every backup. Copy a backup folder tree with a tool that keeps hard links (e.g. `rsync -aH`), or each backup becomes a full copy: see [Off-site copies](offsite.md).
- An interrupted backup leaves `<date>.partial`; the next run continues it.
- Attachments whose file never reached the Zotero server cannot be backed up; they are listed in `meta.json` and by `backup`, so they can be uploaded from the computer that has them.

## Looking into a backup

Without restoring anything, and without the network or an API key:

```bash
zotero-backup find "one health"                  # all libraries below the backup folder
zotero-backup find kruger ~/zotero-backups/group-6719916-Safe_Edit_Test
zotero-backup show ~/zotero-backups/group-6719916-Safe_Edit_Test 9FCKFFL3
zotero-backup show ~/zotero-backups/group-6719916-Safe_Edit_Test/2026-11-05T204338Z 9FCKFFL3 --save ~/Desktop/old
```

- **`find TEXT`** searches every backup for items whose title, creators, DOI, key, note or annotation text or file name contains TEXT (upper and lower case alike). For each item it shows its key and title (from the newest backup that has it), its parent for notes and attachments, and in which backups it is: how many, the first and the last, and whether it is **not in the newest backup** (deleted since) or in the trash there. That is the way to find the backup to restore an item from.
- **`show BACKUP KEY`** prints the item from that backup (`BACKUP` is a backup folder, or a library folder for its newest backup): its fields, creators, tags and collections, then its attachments, notes (as text) and annotations.
- **`--save DIR`** copies the files of the item and its attachments out of the backup into `DIR/<attachment key>/`, ZIPs unpacked. It never overwrites a file and refuses a folder inside the backups.

- **`diff BACKUP1 BACKUP2`** compares two backups of the same library: what was **added**, **removed** and **changed** (with the fields) between them, files changed and settings changed. The order does not matter; parts either backup left out are not compared.

    ```bash
    zotero-backup diff ~/zotero-backups/group-6719916-Safe_Edit_Test/2026-11-05T204338Z ~/zotero-backups/group-6719916-Safe_Edit_Test/2026-11-10T102736Z
    ```

To put an item back into the library: `zotero-backup restore BACKUP --items KEY` ([Restore](restore.md)).
