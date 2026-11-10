# Changelog

What changed for users, per version. The [releases](https://github.com/rkrug/zotero-backup/releases) have the downloads; [Zenodo](https://doi.org/10.5281/zenodo.24090287) archives each one.

## Unreleased

- **`restore --trash-new` / `--erase-new`:** also move everything not in the backup to the Zotero trash, or erase it for good (the trash too), so the library ends up exactly as in the backup. Command line only, not with `--items`; shown in the plan and the question with a count.
- **Fix:** parts a backup left out (`--exclude notes`, `attachments`, `annotations`, `trash`) no longer show as "only in the library, not in the backup".

- **`zotero-backup upgrade [stable|beta]`** updates zotero-backup from the GitHub releases: shows the newest stable and beta release and asks; `--check` only shows; `--yes` without asking; `stable --downgrade` back from a pre-release. Every update is checked (Ed25519 signature over `SHA256SUMS`, then the archive's checksum, then the new version) before anything is replaced. Releases now include `SHA256SUMS.sig`.

- **Documentation per version:** the [site](https://rkrug.github.io/zotero-backup/) keeps the documentation of each release (version menu in the header); `dev` shows the unreleased state.
- **AI assistants:** an Agent Skill (`skills/zotero-backup/`) for using zotero-backup safely through an assistant such as Claude Code, with what each error message means and how to read a stress test report.
- `--help`: `--exclude` also applies to `diff` and `restore` (it leaves the parts out of the plan); the help said backup only.
- **For contributors:** [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md) (private reports), issue templates for bug reports and Windows / Linux test reports, and `CITATION.cff` ("Cite this repository" on GitHub).

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
