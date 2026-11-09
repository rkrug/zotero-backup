# Disclaimer

## No warranty

zotero-backup is provided **"as is", without warranty of any kind**, express or implied (see the [MIT licence](https://github.com/rkrug/zotero-backup/blob/main/LICENSE)). Nobody guarantees that it works correctly, that a backup is complete, or that a restore brings a library back as it was. The authors are not liable for any loss or damage, including lost or changed Zotero data.

## Your responsibility

You use zotero-backup at your own risk and you are responsible for your data:

- **`restore` writes into a Zotero library**: it creates, changes and trashes items, collections, searches, files and settings. Look at what it will do first with `zotero-backup diff` and `zotero-backup restore --dry-run`.
- **`prune` deletes backups.** Check with `zotero-backup prune --dry-run`.
- **Keep other, independent copies** of libraries that matter, for example a copy of the Zotero data folder or an export, and try a restore on a test group before you rely on it.
- **Sync every Zotero app** that works on a library before restoring it (see [Restore](restore.md)).
- **Check your backups**: read the summary and `meta.json` of each backup for missing files and problems.
- **The stress test changes the libraries it runs on.** Run it only on test groups or libraries, ideally of a separate Zotero account (see [Stress test](stresstest.md)).
- **Keep your API keys safe.** A key with write access can change every library it is allowed to write to.

## Not affiliated with Zotero

zotero-backup is an independent project. It is not made, endorsed or supported by Zotero or the Corporation for Digital Scholarship. Zotero is a trademark of the Corporation for Digital Scholarship. Questions and problems with zotero-backup belong in its [issues](https://github.com/rkrug/zotero-backup/issues), not in the Zotero forums.
