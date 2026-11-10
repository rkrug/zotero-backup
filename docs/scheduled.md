# Scheduled backups

Run `backup` and then `prune`, e.g. daily. With no terminal, progress is written as plain lines every `progress_interval` seconds; use `--quiet` for only the summary and errors.

The computer has to be on and online at that time; Zotero desktop does not need to run (backups use the web API only). Use a **read-only** key for scheduled backups, so a key that is stored for unattended runs cannot change any library.

## The quick way

```bash
zotero-backup schedule install --at 02:30    # daily backup, then prune
zotero-backup schedule show                  # when, what it runs, log, state
zotero-backup schedule show raw              # the files themselves and the scheduler's own output
zotero-backup schedule remove
```

It sets up what the sections below describe, with your system's own scheduler, running while you are logged in: a **LaunchAgent** on macOS (`~/Library/LaunchAgents/org.zotero-backup.plist`, log `~/Library/Logs/zotero-backup.log`), a **systemd user timer** on Linux (`~/.config/systemd/user/zotero-backup.timer`, log in the journal) or a **scheduled task** on Windows (log in `%LOCALAPPDATA%\zotero-backup`). The job runs `backup` and then `prune` with progress as log lines, using this program's full path; `--profile` and `--config` are passed on (another profile gets its own job, e.g. `org.zotero-backup.work`). Default time 06:00.

- Store the key first (`zotero-backup key set`): the job reads it from your keychain.
- It never overwrites or removes a setup it did not write itself (e.g. one copied from below by hand): it says so and stops.
- Install zotero-backup in its final place first: the job keeps the path of the program it was set up **with** (`upgrade` replaces the file in place, so the job stays valid). `install` and `show` say so when that is not the `zotero-backup` on your `PATH`; running `schedule install` again with the right one replaces the job.
- For runs **while nobody is logged in**, use the manual setups [below](#when-nobody-is-logged-in).
- A backup started by hand that still runs when the schedule comes up is safe: the scheduled run skips the libraries it is working on ([Backups → One run per library](backups.md)). `zotero-backup status` shows what runs, `zotero-backup stop` stops it.

## By hand

The setups below run while you are logged in (a locked screen is fine). For runs **when nobody is logged in**, see [below](#when-nobody-is-logged-in): the schedule is easy, the key is the point, because a keychain is only unlocked by logging in.

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

## Notifications

A scheduled backup that fails says so only in its log. To hear about it, set in the profile ([Configuration](configuration.md)):

```json
"notify_macos": true,
"notify_command": ["/usr/bin/mail", "-s", "zotero-backup failed", "me@example.org"]
```

- Sent after a backup that **failed** (a library could not be backed up, no key, …) or reported **problems** in `meta.json`; not for attachments whose file never reached the server (`missing_files`), which are common.
- `notify_command` is a program and its arguments, run without a shell, with the summary on stdin and `ZB_STATUS` (`failed`, `problems`) and `ZB_LIBRARIES_FAILED` in the environment; `ZOTERO_API_KEY` is removed from its environment. Any tool works: `mail`, `curl` to [ntfy](https://ntfy.sh) (`["curl", "-T", "-", "https://ntfy.sh/my-topic"]`), a script of your own.
- `notify_macos` shows a macOS notification (Notification Center). Under a LaunchDaemon (nobody logged in) there is no one to show it to: use `notify_command` there.
- A notification that fails is reported in the log and does not change the backup's result.
- Try it: `zotero-backup notify-test`.

## When nobody is logged in

| System | Schedule | API key |
|---|---|---|
| macOS | a LaunchDaemon that runs as you | your login keychain is locked: a key file only you can read |
| Linux | the user timer above with `loginctl enable-linger` | GNOME Keyring is locked: the encrypted systemd credential shown above |
| Windows | the task with "Run whether user is logged on or not" | Credential Manager works, as long as Windows stores your password for the task |

**macOS.** Most Macs are only without a logged-in user after **Log Out**: with FileVault on, nothing runs after a restart until someone unlocks the disk at the login screen, which logs that person in. If the LaunchAgent above is enough for you, keep it.

For runs while logged out, store the read-only key in a file that only you can read (FileVault encrypts it on disk):

```bash
mkdir -p ~/.config/zotero-backup
( umask 077; read -rs "k?API key: "; printf %s "$k" > ~/.config/zotero-backup/key ); echo
```

(`read -rs "k?…"` is zsh, the macOS shell; in bash: `read -rsp "API key: " k`.) Then save the following as `org.zotero-backup.plist`, with `YOU` replaced by your user name, and install it as a LaunchDaemon:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>org.zotero-backup</string>
  <key>UserName</key><string>YOU</string>
  <key>EnvironmentVariables</key><dict><key>HOME</key><string>/Users/YOU</string></dict>
  <key>ProgramArguments</key><array>
    <string>/bin/sh</string><string>-c</string>
    <string>ZOTERO_API_KEY=$(cat "$HOME/.config/zotero-backup/key") &amp;&amp; export ZOTERO_API_KEY &amp;&amp; /usr/local/bin/zotero-backup backup &amp;&amp; /usr/local/bin/zotero-backup prune</string>
  </array>
  <key>StartCalendarInterval</key><dict><key>Hour</key><integer>6</integer><key>Minute</key><integer>0</integer></dict>
  <key>StandardOutPath</key><string>/Users/YOU/Library/Logs/zotero-backup.log</string>
  <key>StandardErrorPath</key><string>/Users/YOU/Library/Logs/zotero-backup.log</string>
</dict></plist>
```

```bash
launchctl unload ~/Library/LaunchAgents/org.zotero-backup.plist 2>/dev/null   # the LaunchAgent, if you had it
sudo cp org.zotero-backup.plist /Library/LaunchDaemons/
sudo chown root:wheel /Library/LaunchDaemons/org.zotero-backup.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/org.zotero-backup.plist
```

It runs as you (the backups belong to you), whether you are logged in or not; a run missed while the Mac was asleep follows when it wakes. `ZOTERO_API_KEY` wins over the keychain, so the key file is used even while you are logged in. To remove it: `sudo launchctl bootout system/org.zotero-backup` and delete the plist.

**Linux.** The user timer already runs without a login once `loginctl enable-linger $USER` is set. The keyring is locked then: use the encrypted systemd credential from the service above instead of `secret-tool`.

**Windows.** Create the task with your user and password, so it runs whether you are logged on or not (`/RP *` asks for the password; Windows stores it for the task):

```bat
schtasks /Create /SC DAILY /ST 06:00 /TN "zotero-backup" /RU "%USERNAME%" /RP * /TR "cmd /c \"C:\Tools\zotero-backup.exe backup && C:\Tools\zotero-backup.exe prune\""
```

The task then logs on as you, so the key in Credential Manager is available. Not with "Do not store password": Windows then cannot open your stored credentials. To catch up on a run missed while the computer was off: Task Scheduler → the task → Settings → "Run task as soon as possible after a scheduled start is missed".
