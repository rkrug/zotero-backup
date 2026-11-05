# zotero-backup

Backup and restore of Zotero libraries (personal library and groups) through the Zotero web API, **keeping the item keys**, so restored items are the same items: citations in Word and LibreOffice documents keep working.

- **Complete backups.** Every backup is a folder with the whole library: items (with notes, annotations and the trash), collections, saved searches, library settings such as tag colours, the full-text index, and the attachment files.
- **Small and fast after the first run.** Files that did not change since the previous backup are hard links to it: not downloaded again, not stored twice.
- **Restore into the same library**, everything or selected items, with a preview first.
- One executable for Windows, macOS and Linux; nothing else to install.

The plan and the tests behind the design are in [Issue #1](https://github.com/rkrug/zotero-backup/issues/1).

## Install

Download the archive for your system from the [releases](https://github.com/rkrug/zotero-backup/releases), unpack it and put `zotero-backup` (`zotero-backup.exe` on Windows) somewhere on your `PATH`. On macOS, the first start of a downloaded binary may need `xattr -d com.apple.quarantine zotero-backup`.

Or build it with Go: `go install github.com/rkrug/zotero-backup@latest`.

## API key

Create a key at <https://www.zotero.org/settings/keys>. For backups a **read-only** key is enough (personal library with notes and files, groups read). Restore needs a key with **write** access; keep that one for restores only.

The key is read from the environment variable `ZOTERO_API_KEY`, or else from the system keychain entry named by the profile's `key` (see [Keys](#keys)). It is never written to the configuration, the backups or the logs, and never taken as a command-line argument.

```bash
zotero-backup key check
```

shows the key's user and what it may do in each library.

## Quick start

```bash
zotero-backup backup                # all libraries the key can read, into ~/zotero-backups
zotero-backup list
zotero-backup diff ~/zotero-backups/group-2333076-IPBES_LDR
zotero-backup restore ~/zotero-backups/group-2333076-IPBES_LDR --dry-run
zotero-backup prune --dry-run
```

## Configuration

A JSON file, by default in the user configuration folder (`~/Library/Application Support/zotero-backup/config.json` on macOS, `~/.config/zotero-backup/config.json` on Linux, `%AppData%\zotero-backup\config.json` on Windows), or given with `--config`. Without a file, the defaults apply. `zotero-backup config` prints the configuration in effect, with every option.

Profiles are chosen with `--profile NAME` (default: `default`). Options a profile leaves out keep their default:

```json
{
  "profiles": {
    "default": {
      "library": "all",
      "key": "zotero-backup",
      "to": "~/zotero-backups",
      "exclude": [],
      "write_key": "",
      "keep_daily": 7,
      "keep_weekly": 5,
      "keep_monthly": 12,
      "progress": "auto",
      "progress_interval": 30
    },
    "ldr": { "library": "group:2333076", "key": "zotero-readonly" },
    "mine": {
      "key": "readonly",
      "write_key": "zotero-write",
      "keys": { "group:6719916": "safe-edit-write", "user": "personal" }
    }
  }
}
```

| Option | Meaning |
|---|---|
| `library` | `all` (the personal library and every group the key can read), `user`, or `group:<id>` |
| `key` | name of the keychain entry holding the API key (the default key) |
| `keys` | per library, a different keychain entry: `"user"`, `"group:<id>"` or `"default"` → name. With `library: all`, the default key lists the libraries; a library listed here is read with its own key, also if the default key cannot read it. `diff` and `restore` use the key for the backup's library, so a write key can be kept for the libraries that need restores. `--key NAME` uses one entry for everything in a run |
| `write_key` | keychain entry `restore` uses for every library (e.g. a key with write access), while `backup` and `diff` keep the read-only `key` / `keys`; empty: restore uses the library's key |
| `to` | folder for the backups |
| `exclude` | parts to leave out of backup and restore: `files` (records kept, file contents skipped), `attachments`, `notes`, `annotations`, `fulltext`, `settings`, `trash`; `--exclude a,b` overrides it for one run |
| `keep_daily`, `keep_weekly`, `keep_monthly` | `prune` keeps the newest backup of each of the last N days, ISO weeks and months (three generations); the newest backup is always kept |
| `progress` | `auto` (an updating line on a terminal, plain lines otherwise), `line`, `log` or `quiet`; `--progress` / `--quiet` override it |
| `progress_interval` | seconds between progress lines in log mode |

Unknown options and invalid values are errors, so a typo does not go unnoticed.

## Backups

```
~/zotero-backups/
  group-2333076-IPBES_LDR/
    2026-11-05T190523Z/
      meta.json          library version, counts, files missing on the server, problems
      items.json  collections.json  searches.json  settings.json  fulltext.json
      files.json         attachment key → file name, md5, size
      files/<attachment key>/<file name>
    2026-11-06T060000Z/  …complete as well
  user-12022430-Rainer_M_Krug/
```

- Folder names are `group-<id>-<name>` and `user-<id>-<username>` (spaces as `_`). A renamed group keeps its folder: it is found by ID and renamed.
- Backup names are the time in **UTC** (`YYYY-MM-DDTHHMMSSZ`, no colons so they work on Windows), independent of the computer's time zone and of summer time; they sort by time.
- A file that did not change since the previous backup (same md5) is a **hard link**: deleting a backup frees only the files no other backup links to. On drives without hard links (FAT/exFAT, some network shares) files are copied instead. Never edit files inside a backup: a hard-linked file is the same file in every backup. Copy a backup folder tree with a tool that keeps hard links (e.g. `rsync -aH`), or each backup becomes a full copy.
- An interrupted backup leaves `<date>.partial`; the next run continues it.
- Attachments whose file never reached the Zotero server cannot be backed up; they are listed in `meta.json` and by `backup`, so they can be uploaded from the computer that has them.

## Restore

```bash
zotero-backup diff BACKUP                     # what differs from the library today
zotero-backup restore BACKUP --dry-run        # what restore would do
zotero-backup restore BACKUP                  # shows the plan, asks, restores, checks
zotero-backup restore BACKUP --items ABCD1234,EFGH5678
```

`BACKUP` is a backup folder, or a library folder for its newest backup. Restore writes into the **same library** the backup came from: missing items, collections and searches are recreated with their old keys, changed ones set back, items taken out of (or put back into) the trash, files uploaded (or attached again if Zotero still has them), tag colours set back. Items added since the backup are kept. `--items` restores only these items, with their children (attachments, notes, annotations), their parents and the collections they need. At the end the library is read again and compared with the backup.

**Before a restore, sync every Zotero app that works on the library.** A Zotero app with unsynced changes shows a conflict dialog on its next sync, and whatever is chosen there wins over the restore. If that happened, run `restore` again; or, on that computer, use Zotero → Settings → Sync → Reset → **Restore from Online Library** (not "Replace Online Library", which goes the other way).

Restoring files counts against the library owner's Zotero storage.

## Scheduled backups

Run `backup` and then `prune`, e.g. daily. With no terminal, progress is written as plain lines every `progress_interval` seconds; use `--quiet` for only the summary and errors.

**macOS** (launchd): save as `~/Library/LaunchAgents/org.zotero-backup.plist`, then `launchctl load ~/Library/LaunchAgents/org.zotero-backup.plist`. Runs as you, so the keychain entry is available.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>org.zotero-backup</string>
  <key>ProgramArguments</key><array>
    <string>/bin/sh</string><string>-c</string>
    <string>/usr/local/bin/zotero-backup backup &amp;&amp; /usr/local/bin/zotero-backup prune</string>
  </array>
  <key>StartCalendarInterval</key><dict><key>Hour</key><integer>6</integer><key>Minute</key><integer>0</integer></dict>
  <key>StandardOutPath</key><string>/tmp/zotero-backup.log</string>
  <key>StandardErrorPath</key><string>/tmp/zotero-backup.log</string>
</dict></plist>
```

**Linux** (systemd timer, as your user): `~/.config/systemd/user/zotero-backup.service`

```ini
[Service]
Type=oneshot
ExecStart=/usr/local/bin/zotero-backup backup
ExecStartPost=/usr/local/bin/zotero-backup prune
# On a server without a keychain, give the key as an encrypted systemd credential
# (user services need systemd 256 or newer and `systemd-creds --user`):
#   systemd-creds --user encrypt --name=zotero - ~/.config/zotero-backup/key.cred   (type the key, then Ctrl-D)
# LoadCredentialEncrypted=zotero:%h/.config/zotero-backup/key.cred
# ExecStart=/bin/sh -c 'ZOTERO_API_KEY=$(cat "$CREDENTIALS_DIRECTORY/zotero") exec /usr/local/bin/zotero-backup backup'
```

and `~/.config/systemd/user/zotero-backup.timer`

```ini
[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

then `systemctl --user enable --now zotero-backup.timer` (and `loginctl enable-linger $USER` on a server, so it runs without a login). With cron instead: `0 6 * * * /usr/local/bin/zotero-backup backup --quiet && /usr/local/bin/zotero-backup prune`.

**Windows** (Task Scheduler), in a command prompt:

```bat
schtasks /Create /SC DAILY /ST 06:00 /TN "zotero-backup" /TR "cmd /c \"C:\Tools\zotero-backup.exe backup && C:\Tools\zotero-backup.exe prune\""
```

## Keys

Keys are stored in the system keychain under a name; the configuration only holds the name (the profile's `key`, default `zotero-backup`):

```bash
zotero-backup key set              # asks for the key (no echo), checks it with Zotero, stores it as "zotero-backup"
zotero-backup key set readonly     # …under another name; use it with "key": "readonly" in a profile
zotero-backup key check readonly   # user, libraries and rights of that key
zotero-backup key check            # every key the profile uses, and what each is for
```

A read-only key as `key` and a write key as `write_key` keep scheduled backups from ever changing a library, while `restore` uses the write key without further options:

```bash
zotero-backup key set zotero-write    # then "write_key": "zotero-write" in the profile
zotero-backup key remove readonly  # deletes the keychain entry (the key stays valid at zotero.org)
```

A key can also be piped in (`… | zotero-backup key set`), e.g. from a password manager.

| System | Keychain |
|---|---|
| macOS | Keychain (entry: service = name) |
| Windows | Credential Manager (generic credential, target = name) |
| Linux desktop | Secret Service: GNOME Keyring, KWallet, KeePassXC, through `secret-tool` (package `libsecret-tools` or `libsecret`) |
| Linux server | usually no keychain: set `ZOTERO_API_KEY`, e.g. from an encrypted systemd credential (see above) |

`ZOTERO_API_KEY` always wins over the keychain.

## Not covered

- Linked files (only the link is on the Zotero server).
- What exists only in the Zotero app: feeds, plugins, styles, preferences. Copy the Zotero data folder for that.
- Restoring into a *different* library: keys could be kept, but citation links contain the library ID.

## Development

```bash
go vet ./... && go test -race ./...
go build -o zotero-backup .
.github/build.sh v0.0.0-test     # release archives for all systems into dist/
```

Releases are built by GitHub Actions for each tag `v*`. See `CLAUDE.md` for the structure.

## Licence

MIT, see [LICENSE](LICENSE).
