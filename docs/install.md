# Install

Download the archive for your system from the [releases](https://github.com/rkrug/zotero-backup/releases), unpack it and put `zotero-backup` (`zotero-backup.exe` on Windows) somewhere on your `PATH`.

Or build it with Go: `go install github.com/rkrug/zotero-backup@latest`.

Check the version with `zotero-backup version`.

!!! note "Windows and Linux: not yet tried on a real system"
    Only the macOS version has been checked by hand so far; the Windows and Linux versions pass the unit tests in CI but are untested in real use ([#15](https://github.com/rkrug/zotero-backup/issues/15), [#16](https://github.com/rkrug/zotero-backup/issues/16)). See [Platform status](index.md).

## macOS: the first start is blocked

zotero-backup is not signed and notarised by Apple, so macOS blocks a binary downloaded with a browser on its first start: a dialog says it was "Not Opened" because Apple could not check it for malicious software. Click **Done** (not "Move to Trash"), then either

- open System Settings → **Privacy & Security**, scroll down to the message about `zotero-backup` and click **Open Anyway**; start it again and confirm with **Open** (with your password or Touch ID), or
- remove the download's quarantine flag in Terminal, in the folder with the binary:

    ```bash
    xattr -d com.apple.quarantine zotero-backup
    ```

Either is needed once per downloaded binary. Archives downloaded with `curl` or `gh release download` carry no quarantine flag and start without this.

## API key

Create a key at <https://www.zotero.org/settings/keys>. For backups a **read-only** key is enough (personal library with notes and files, groups read). Restore needs a key with **write** access; keep that one for restores only.

The key is read from the environment variable `ZOTERO_API_KEY`, or else from the system keychain entry named by the profile's `key` (see [Keys](keys.md)). It is never written to the configuration, the backups or the logs, and never taken as a command-line argument.

```bash
zotero-backup key check
```

shows the key's user and what it may do in each library.
