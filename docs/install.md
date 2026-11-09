# Install

Download the archive for your system from the [releases](https://github.com/rkrug/zotero-backup/releases), unpack it and put `zotero-backup` (`zotero-backup.exe` on Windows) somewhere on your `PATH`. On macOS, the first start of a downloaded binary may need `xattr -d com.apple.quarantine zotero-backup`.

Or build it with Go: `go install github.com/rkrug/zotero-backup@latest`.

Check the version with `zotero-backup version`.

## API key

Create a key at <https://www.zotero.org/settings/keys>. For backups a **read-only** key is enough (personal library with notes and files, groups read). Restore needs a key with **write** access; keep that one for restores only.

The key is read from the environment variable `ZOTERO_API_KEY`, or else from the system keychain entry named by the profile's `key` (see [Keys](keys.md)). It is never written to the configuration, the backups or the logs, and never taken as a command-line argument.

```bash
zotero-backup key check
```

shows the key's user and what it may do in each library.
