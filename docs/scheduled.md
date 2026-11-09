# Scheduled backups

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
