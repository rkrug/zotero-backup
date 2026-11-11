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
- **One run per library at a time.** While a backup or prune works on a library, its folder holds a `.lock` file (process ID, computer, program, start time, command). A second run that finds it skips that library with "a backup of … is already running (PID …)" and counts it as skipped (not failed: the other run is making that backup); the other libraries go on. A lock left by a run that no longer runs on this computer (a crash, a power cut) is taken over by the next run, with a warning; a lock from another computer (a backup folder on a shared disk) never is: the message says which file to remove once no backup runs there.
- Attachments whose file never reached the Zotero server cannot be backed up; they are listed in `meta.json` and by `backup`, so they can be uploaded from the computer that has them.

## The result of a run

`backup` ends with one line that sums up the run, also when libraries failed or the run was stopped:

```
backup finished: 19 libraries (19 backed up, 0 failed, 0 skipped), 0 problems, 412 missing files (0 new), 3 files downloaded (12.4 MB), 15233 linked, 1h12m5s
backup failed: 19 libraries (17 backed up, 1 failed, 1 skipped), 2 problems, … ; failed: group-2333076 "IPBES LDR"; skipped: group-5081872 "IPBES BBA" (running: PID 4242 on server, …)
backup stopped: … ; stopped: group-2333076 "IPBES LDR"
```

- **skipped:** another run on this computer is backing up that library ([One run per library](#running-and-stopping)), or a key in `keys` could not be used. **without access** (shown only when there are some): libraries the key may not read, normal with `library: all`; they are not counted.
- **missing files (N new):** attachments whose file is not on the Zotero server; *new* are those not missing in the previous backup of the library (often a file still being uploaded from someone's computer).
- **Exit status:** 0 when every library was backed up or skipped (problems and missing files included: see the line), 1 when a library failed, nothing could be read (no key, no network) or the run was stopped.
- With `--quiet`: only this line, failed libraries and warnings.

The same, per library, is written to **`last-run.json`** in the backup folder (`~/zotero-backups/last-run.json`), replaced by every run: start, end, `status` (`ok`, `problems`, `failed`, `stopped`), `exit_status`, the error if nothing could be read, and per library the result, backup folder, version, counts, files downloaded and linked, missing files with the new ones listed, and the problems. For monitoring tools and scripts; it never contains an API key. `zotero-backup schedule show` shows its line as `last run`.

## Checking backups

```bash
zotero-backup verify                 # the newest backup of each library
zotero-backup verify all             # every backup (files shared by hard links are read once)
zotero-backup verify ~/zotero-backups/group-6719916-Safe_Edit_Test   # one library's newest, or a backup folder
zotero-backup verify --quick         # sizes only, no checksums: fast, e.g. daily; the full check e.g. weekly
```

Offline, without a key. For each backup:

- the JSON files can be read, and (for backups made with v0.3.1 or later) are unchanged since the backup: their md5 is recorded in `meta.json`;
- the counts (items per type, collections, searches, full text, files) match `meta.json`;
- every file is there, has its size and its md5 (for a ZIP, the md5 of the file inside, as Zotero records it);
- every attachment whose file is on the Zotero server is backed up or listed in `missing_files`;
- a leftover `.partial` folder is mentioned (not a problem).

One line per backup, `OK` or `PROBLEMS` with each problem; the exit status is 0 only if all are fine. A damaged backup is best replaced: the next `backup` makes a complete one again, and `prune` removes the damaged one in time (or delete it by hand).

## Running and stopping

```bash
zotero-backup status                 # the backups running now: library, PID, computer, since, command
zotero-backup stop                   # stop every backup running on this computer in the backup folder
zotero-backup stop group:2333076     # only the one of that library (or: stop user)
```

`stop` asks the run to stop as `Ctrl-C` would and waits until it has ended: the finished libraries stay complete, the unfinished one stays as `<date>.partial` for the next run, and its lock is removed. It only stops a process that really is the zotero-backup run of the lock, and never a run on another computer. (On Windows the run is ended hard; its `.partial` is continued all the same.)

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
