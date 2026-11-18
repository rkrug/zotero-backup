# Off-site copies

Backups on one disk are lost with that disk (or the computer, or the house). Keep a second copy elsewhere: an external drive, a NAS, another computer.

The one thing to know: **unchanged files are hard links** between backups ([Backups](backups.md)). A copy that does not keep them stores every file once per backup, so a folder of 30 backups needs about 30 times the space. Copy the whole backup folder (`~/zotero-backups`) at once, with a tool that keeps hard links.

## rsync (macOS, Linux)

```bash
rsync -aH --delete ~/zotero-backups/ /Volumes/BackupDisk/zotero-backups/
```

- `-a` keeps dates and permissions, **`-H` keeps the hard links**, `--delete` removes backups that `prune` removed (leave it out to keep every backup on the copy).
- The trailing `/` matters: it copies the folder's content, not the folder into itself.
- To a NAS or another computer over SSH: `rsync -aH --delete ~/zotero-backups/ me@nas:zotero-backups/`.
- Checked with the `rsync` that comes with macOS (openrsync, macOS 27), with rsync 3 (Homebrew) and with `tar`: all keep the links. **Finder, `cp -R` and Windows Explorer do not.**
- Run it after the scheduled backup, e.g. as a second step of your own schedule, or by hand.

On Windows, `robocopy` does not keep hard links; copy from WSL with `rsync -aH`, or keep an archive (below).

## Archives

`tar` keeps hard links, so an archive is as small as the backup folder:

```bash
tar -czf zotero-backups-$(date +%Y%m%d).tar.gz -C ~ zotero-backups
```

An archive is one file: it suits a cloud folder, a USB stick or a drive formatted FAT/exFAT (which knows no hard links).

## Backup software and cloud folders

- **Time Machine and similar tools** back up `~/zotero-backups` like any other folder. Whether they keep the hard links depends on the tool: check the space the backups take there after a few runs.
- **Cloud folders** (iCloud Drive, Dropbox, OneDrive, Google Drive) store every hard link as a file of its own: do not put the backup folder into one. Put an archive there instead, or only the newest backup: `latest_dir` (below) keeps it at a fixed path.

## Backup programs that do not know hard links

Backblaze Personal and similar programs back up every path as a file of its own. Each backup is a new dated folder, so they read (and may upload) every file again each night, although on disk it is the same file.

`latest_dir` ([Configuration](configuration.md)) avoids that: after each backup, zotero-backup makes `<latest_dir>/<library folder>/` a hard-link copy of the library's newest backup. The paths stay the same from one night to the next, and a file that did not change stays the same file, so such a program uploads only what changed (new and changed files, the JSON files).

```json
{ "profiles": { "default": { "to": "/Volumes/Data/zotero-backup", "latest_dir": "/Volumes/Data/zotero-latest" } } }
```

- Create the folder once (`mkdir -p /Volumes/Data/zotero-latest`); it is never created, like `to`.
- It must be on the **same drive** as `to` (hard links); it takes almost no space.
- Exclude `to` in the backup program (Backblaze: Settings → Exclusions; that covers every backup folder made later) and leave `latest_dir` included.
- The copy off-site then has the newest state (and whatever older versions the program keeps); the history stays in `to`.
- A library no longer backed up keeps its folder in `latest_dir` until you remove it.

## Encryption

Backups contain your whole library: notes, PDFs, annotations. A copy that leaves the house belongs on an encrypted disk or volume: an encrypted APFS volume or disk image (Disk Utility) on macOS, BitLocker on Windows, LUKS on Linux, or an encrypted NAS share. zotero-backup does not encrypt itself.

## Checking a copy

- `zotero-backup list` and `find` work on a copy as well (`--config` with a profile whose `to` is the copy, or `find TEXT LIBRARY_FOLDER` with a library folder of the copy).
- **`zotero-backup verify all`** with the copy as the backup folder (`--config` with a profile whose `to` is the copy, or `verify LIBRARY_FOLDER` on the copy) reads every file of the copy and checks its md5 against the backup's own records ([Checking backups](backups.md#checking-backups)).
- `zotero-backup diff COPY ORIGINAL` (two backup folders) compares the copy's metadata with the original's: "No differences." means the same items, collections, settings and file checksums.
- Restoring from a copy works like from the original: `restore` reads only the backup folder you give it.
