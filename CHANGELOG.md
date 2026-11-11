# Changelog

What changed for users, per version. The [releases](https://github.com/rkrug/zotero-backup/releases) have the downloads; [Zenodo](https://doi.org/10.5281/zenodo.24090287) archives each one.

## Unreleased

- **The result of a backup run** (#24): `backup` ends with one summary line, also when libraries failed or the run was stopped (Ctrl-C, `zotero-backup stop`): libraries backed up / failed / skipped, problems, missing files and how many are new since the previous backup, files downloaded and linked, the duration, and which libraries failed or were skipped and why. Libraries the key may not read (`library: all`) are counted apart as "without access". The same per library goes into `last-run.json` in the backup folder, for monitoring; `schedule show` shows it as "last run"; a notification starts with it; with `--quiet` only this line, failures and warnings remain.
- A library skipped because another run is backing it up no longer counts as failed (no exit status 1, no notification).
- Documentation (Scheduled backups, Over SSH): on macOS the first scheduled run asks for access to a backup folder on another volume (or in Documents, Desktop, Downloads) and waits for the answer; how to see it and how to allow it beforehand (Full Disk Access).

## v0.3.1 (2026-11-11)

- **Stress test:** every backup it makes is checked with `verify`; new extras for `diff` of two backups, `find`, `show --save` and the lock. Libraries in Zotero desktop are recognised by ID only, so a renamed account or group no longer stops it.
- **`zotero-backup verify [BACKUP | LIBRARY_FOLDER | all] [--quick]`** (#23) checks backups on disk, offline: the JSON files, the counts, every file's size and md5 (ZIPs: the file inside), attachments backed up or listed as missing; files hard-linked between backups are read once. New backups record the md5 of their JSON files in `meta.json`, so changes to those are found too.
- **One run per library at a time** (#43): a backup or prune holds a lock in the library's folder; a second run (e.g. the schedule while a backup started by hand still runs) skips that library instead of writing into the same unfinished backup. Stale locks are taken over, locks from another computer never. **`zotero-backup status`** shows the runs in progress, **`zotero-backup stop [LIBRARY]`** stops them cleanly (the unfinished backup is continued by the next run). `restore` warns when its library is being backed up.
- Documentation (Install, README, Over SSH): install v0.3.0, then `upgrade stable` to the newest: the same commands for every release, the update signed; `~/bin` on the `PATH` as a step of its own. Over SSH now tried on macOS.
- `schedule show` names the profile the job uses (also `default`) and its configuration file.

## v0.3.0 (2026-11-10)

It includes the pre-releases v0.3.0-rc.1 to rc.3.

**Restore**

- **`restore --trash-new` / `--erase-new`:** also move everything not in the backup to the Zotero trash, or erase it for good (the trash too), so the library ends up exactly as in the backup. Command line only, not with `--items`; shown in the plan and in the question with a count.
- **Fix:** parts a backup left out (`--exclude notes`, `attachments`, `annotations`, `trash`) no longer count as "only in the library, not in the backup" (with the options above they would have been removed).
- `--exclude` also applies to `diff` and `restore` (it leaves those parts out of the plan); the help said backup only.

**Looking into backups** (offline, no key)

- **`find TEXT`** searches the backups for items by title, creators, DOI, key, note text or file name: in which backups each is, and whether it is gone from the newest.
- **`show BACKUP KEY`** prints an item from a backup with its notes, attachments and annotations; `--save DIR` copies its files out.
- **`diff BACKUP1 BACKUP2`** compares two backups of a library: added, removed, changed, files, settings.

**Scheduled backups**

- **`schedule install [--at HH:MM] | show [raw] | remove`** sets up the daily backup (then prune) with the system's scheduler: a LaunchAgent on macOS, a systemd user timer on Linux, a scheduled task on Windows; never touches a setup made by hand.
- **Notifications** after a backup that failed or reported problems: `notify_command` (any program, summary on stdin, never an API key) and `notify_macos`; `zotero-backup notify-test`.
- Log lines before the first library no longer start with ": ".

**Updating**

- **`zotero-backup upgrade [stable|beta]`** updates zotero-backup from the GitHub releases: shows the newest stable and beta release and asks; `--check` only shows; `--yes` without asking; `stable --downgrade` back from a pre-release. Every update is checked (Ed25519 signature over `SHA256SUMS`, then the archive's checksum, then the new version) before anything is replaced. Releases now include `SHA256SUMS.sig`. (Updating to v0.3.0 itself is by hand: v0.2.0 has no `upgrade`.)

**Documentation**

- **Per version:** the [site](https://rkrug.github.io/zotero-backup/) keeps the documentation of each release (version menu in the header); pre-releases and `dev` are marked as newer than the latest release.
- **New pages:** Over SSH (setting up another computer, a read-only key only, restoring from a copy), Off-site copies (rsync / tar keep the hard links, cloud folders do not; encryption; checking a copy).
- **Keys:** create two keys, a read-only one for backups and a read/write one for restores, and store both.
- **AI assistants:** an Agent Skill (`skills/zotero-backup/`) for using zotero-backup safely through an assistant such as Claude Code, with what each error message means and how to read a stress test report.

**For contributors:** [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md) (private reports), issue templates for bug reports and Windows / Linux test reports, `CITATION.cff` ("Cite this repository" on GitHub), Dependabot for the GitHub Actions.

## v0.2.0 (2026-11-09)

The first normal release. It includes the pre-release v0.2.0-rc.1.

**Restore**

- **Zipped attachments:** Zotero desktop uploads snapshots, text files and attachments with several files as a ZIP. Backups now keep that ZIP as `<name>.zip` (`"zip": true` in `files.json`) and restore uploads it as Zotero does; before, restore uploaded the ZIP as the file itself, and Zotero desktop got a ZIP in place of the page. Backups made with v0.1.0-rc.1 are recognised and restore correctly.
- **Subcollections** of a collection that restore takes out of the trash now come back in Zotero desktop too (it had kept them in its own trash).
- Fields are compared without order where Zotero has none (collections, tags), and empty equals absent: no more false differences in `diff`.
- Library names are shown next to their IDs in `diff`, `restore` and the backup summaries.

**Documentation**

- A documentation site: <https://rkrug.github.io/zotero-backup/>, with a no-warranty notice on every page and a [disclaimer](https://rkrug.github.io/zotero-backup/disclaimer/).
- Quick start begins with creating and storing the API keys.
- What restore sets back and what it keeps, including when Zotero desktop shows its conflict window.
- Scheduled backups that run while nobody is logged in (macOS, Windows, Linux).
- macOS: the first start of the downloaded binary is blocked (not notarised); how to allow it.
- Platform status: tested on macOS; Windows and Linux are built and unit-tested in CI but not yet tried on real systems.

**Stress test** (for developers, `zotero-backup stresstest`): an end-to-end test against the real Zotero API and a running Zotero desktop, only on libraries the write key may write to, with a question per library; `--cleanup RUN` removes the test objects of a run that stopped early.

## v0.1.0-rc.1 (2026-11-05)

The first pre-release.

- `backup`: complete, dated backups of the personal library and groups (items, collections, saved searches, settings, full text, files); unchanged files are hard links to the previous backup; interrupted runs resume.
- `list`, `prune` (newest per day, ISO week and month), `diff` and `restore` (into the same library, keeping the item keys; everything or selected items; `--dry-run`).
- API keys in the system keychain on macOS, Windows and Linux (`key set`, `key check`, `key remove`), or `ZOTERO_API_KEY`.
- Configuration profiles; a key per library (`keys`) and a separate key for restores (`write_key`).
- Progress on stderr; release archives for macOS, Windows and Linux.
