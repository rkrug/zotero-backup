# Another computer over SSH

Setting up zotero-backup on another computer from a terminal, over SSH: install, store the key, set up the daily backup. Written for an account that is **logged in on that computer** (at its screen, or with automatic login), e.g. a Mac mini with a server account that is always logged in. For a computer where nobody is logged in, see [Scheduled backups → When nobody is logged in](scheduled.md#when-nobody-is-logged-in).

Log in over SSH **as the account that will own the backups**: the program, the key and the schedule all belong to that account.

```bash
ssh server@macmini.local
```

!!! note "What has been tried"
    Each step is what zotero-backup does on macOS (install with `curl`, `key set`, `schedule install`), checked there locally. The whole workflow over SSH, and the Linux and Windows parts, are written from how those systems work but not yet tried on a real system ([#15](https://github.com/rkrug/zotero-backup/issues/15) Windows, [#16](https://github.com/rkrug/zotero-backup/issues/16) Linux). Reports are welcome.

## 1. Install

Find out which archive fits: `uname -sm` prints e.g. `Darwin arm64` (→ `darwin-arm64`), `Darwin x86_64` (→ `darwin-amd64`), `Linux x86_64` (→ `linux-amd64`), `Linux aarch64` (→ `linux-arm64`). Then, with the version from the [releases](https://github.com/rkrug/zotero-backup/releases):

```bash
VERSION=v0.3.0 SYS=darwin-arm64
cd /tmp
curl -LO "https://github.com/rkrug/zotero-backup/releases/download/$VERSION/zotero-backup-$VERSION-$SYS.tar.gz"
curl -LO "https://github.com/rkrug/zotero-backup/releases/download/$VERSION/SHA256SUMS"
shasum -a 256 -c --ignore-missing SHA256SUMS      # Linux: sha256sum -c --ignore-missing SHA256SUMS
tar -xzf "zotero-backup-$VERSION-$SYS.tar.gz"
mkdir -p ~/bin && mv "zotero-backup-$VERSION-$SYS/zotero-backup" ~/bin/
~/bin/zotero-backup version
```

**Windows** (PowerShell over OpenSSH; `curl.exe` and `tar` come with Windows 10 and 11):

```powershell
$V = "v0.3.0"; $S = "windows-amd64"
cd $env:TEMP
curl.exe -LO "https://github.com/rkrug/zotero-backup/releases/download/$V/zotero-backup-$V-$S.zip"
curl.exe -LO "https://github.com/rkrug/zotero-backup/releases/download/$V/SHA256SUMS"
(Get-FileHash "zotero-backup-$V-$S.zip" -Algorithm SHA256).Hash.ToLower()   # must equal its line in SHA256SUMS
tar -xf "zotero-backup-$V-$S.zip"
mkdir -Force $HOME\bin; move "zotero-backup-$V-$S\zotero-backup.exe" $HOME\bin\
[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";$HOME\bin", "User")
```

(the `Path` change counts from the next login).

- The checksum line must say `OK` for the archive.
- `~/bin` must be on the `PATH` (`echo $PATH`); if not, add `export PATH="$HOME/bin:$PATH"` to `~/.zshrc` (macOS) or `~/.profile` (Linux) and log in again.
- A file downloaded with `curl` has no quarantine flag: macOS does not block its first start (unlike a browser download, see [Install](install.md)).
- Later versions: `zotero-backup upgrade stable` over SSH ([Updating](install.md#updating)). It replaces the file in place, so the schedule keeps working.

## 2. Store the key

!!! warning "Only a read-only key here"
    A computer you set up over SSH runs unattended, can be reached from elsewhere, and keeps its keychain unlocked for a long time. Store **only a read-only key** there: if the computer or its account is ever misused, the key can read your libraries but never change or delete anything in them. No `write_key` in its profile.

    Create the read-only key at <https://www.zotero.org/settings/keys>: *Allow library access* and *Allow notes access*, **not** *Allow write access*, Default Group Permissions **Read Only** ([Keys → Two keys](keys.md#two-keys-one-to-back-up-one-to-restore)). Keep the read/write key for restores on the computer you work on.

`key set` asks for the key without showing it; over SSH the typed key travels encrypted and never lands in a command line or the shell history. `key check` shows what the key may do: it must say read-only for every library.

**macOS.** The login keychain is unlocked while the account is logged in, but an SSH session often cannot use it until it is unlocked there too. Unlock it (it asks for the account's password), then store and check the key:

```bash
security unlock-keychain ~/Library/Keychains/login.keychain-db
zotero-backup key set
zotero-backup key check
```

Without the unlock, `key set` fails with a message like "User interaction is not allowed".

**Linux.** The keyring (GNOME Keyring, KWallet) belongs to the desktop session; an SSH session usually cannot reach it, even while you are logged in at the screen. Run `key set` in a terminal on the computer itself, or, on a server without a desktop, give the key as an encrypted systemd credential instead ([Scheduled backups](scheduled.md), the Linux service).

**Windows** (OpenSSH server, unchecked): `zotero-backup key set` stores the key in the Credential Manager of the SSH session's account. Windows may refuse that in an SSH session that logged in with an SSH key instead of the account's password; then log in with the password, or run `key set` at the computer.

## 3. A first backup

```bash
zotero-backup backup
zotero-backup list
```

The backups go to `~/zotero-backups` of that account (`zotero-backup config` shows the folder; change it in the [configuration](configuration.md)). Before the first scheduled run, look at what `prune` would delete:

```bash
zotero-backup prune --dry-run
```

## 4. The daily backup

```bash
zotero-backup schedule install --at 02:30
zotero-backup schedule show
```

`schedule show` must name `~/bin/zotero-backup` (the program you installed); if it says the job's program is not the `zotero-backup` on your `PATH`, run `schedule install` again with the right one ([Scheduled backups → The quick way](scheduled.md#the-quick-way)).

- **macOS:** a LaunchAgent in the account's login session. Over SSH this works because the account is logged in at the screen; if it is not, `launchctl` refuses and the job cannot run.
- **Linux:** a systemd user timer. It runs while the account has a session; for runs at night without one, `loginctl enable-linger $USER` (once, may need `sudo`). With the key as a systemd credential, use the service from [Scheduled backups](scheduled.md) instead of `schedule install`.
- **Windows** (unchecked): a scheduled task for the SSH session's account, which runs while that account is logged on.

Check the next day:

```bash
zotero-backup list
zotero-backup schedule show
tail -50 ~/Library/Logs/zotero-backup.log        # macOS; Linux: journalctl --user -u zotero-backup
```

To hear about failures without logging in: `notify_command` in the profile, e.g. mail or [ntfy](https://ntfy.sh) ([Scheduled backups → Notifications](scheduled.md#notifications)).

## Restoring

Restore on the computer you work on, with the read/write key stored there, from a copy of the server's backups ([Off-site copies](offsite.md), e.g. `rsync -aH server@macmini.local:zotero-backups/ ~/zotero-backups-server/`): `restore` reads only the backup folder you give it. If a restore must run on the server itself, store the write key there only for that (`zotero-backup key set zotero-write`, then `restore … --key zotero-write`), remove it afterwards (`zotero-backup key remove zotero-write`), and delete that key at zotero.org.

## A computer that is always logged in

- **The keychain must stay unlocked.** By default the macOS login keychain does not lock on its own; if it is set to lock after some time or on sleep (Keychain Access → login keychain → Change Settings), scheduled runs fail with "no API key … the keychain could not be read". A [notification](scheduled.md#notifications) tells you.
- **After a restart** nothing runs until the account is logged in again: automatic login (System Settings → Users & Groups), and with FileVault on, someone must unlock the disk at the login screen first.
- **Sleep:** a run missed while the computer slept follows when it wakes; for a server, turn off sleep or schedule a wake (System Settings → Energy).
- **Disk space:** the backups grow with every changed file; `prune` keeps the newest of each of the last 7 days, 5 weeks and 12 months by default. A second copy elsewhere: [Off-site copies](offsite.md).
