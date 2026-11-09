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
- A file that did not change since the previous backup (same md5) is a **hard link**: deleting a backup frees only the files no other backup links to. On drives without hard links (FAT/exFAT, some network shares) files are copied instead. Never edit files inside a backup: a hard-linked file is the same file in every backup. Copy a backup folder tree with a tool that keeps hard links (e.g. `rsync -aH`), or each backup becomes a full copy.
- An interrupted backup leaves `<date>.partial`; the next run continues it.
- Attachments whose file never reached the Zotero server cannot be backed up; they are listed in `meta.json` and by `backup`, so they can be uploaded from the computer that has them.
