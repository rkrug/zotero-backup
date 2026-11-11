# Configuration

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
| `notify_command` | a program with its arguments, run after a backup that **failed or reported problems**, e.g. `["/usr/bin/mail", "-s", "zotero-backup", "me@example.org"]`; no shell. It gets the backup's summary on stdin (first the summary line) and `ZB_STATUS` (`failed` or `problems`) and `ZB_LIBRARIES_FAILED` in its environment, never an API key. See [Scheduled backups](scheduled.md#notifications) |
| `notify_macos` | `true`: also a macOS notification then |

Unknown options and invalid values are errors, so a typo does not go unnoticed.
