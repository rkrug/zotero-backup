# Keys

## Two keys: one to back up, one to restore

Create **two** API keys at <https://www.zotero.org/settings/keys> (**Create new private key**), one for each job:

| Key | Settings at zotero.org | Used by |
|---|---|---|
| **read-only**, e.g. "zotero-backup read" | Personal Library: *Allow library access* and *Allow notes access*, **not** *Allow write access*; Default Group Permissions: **Read Only** | `backup`, `list`, `diff`, scheduled backups |
| **read/write**, e.g. "zotero-backup restore" | the same plus *Allow write access*; Default Group Permissions: **Read/Write** | `restore` only |

Store **both** on the computer you work and restore on, under two names, and tell the profile which is which:

```bash
zotero-backup key set                  # the read-only key, stored as "zotero-backup" (the profile's "key")
zotero-backup key set zotero-write     # the read/write key; then "write_key": "zotero-write" in the profile
zotero-backup key check                # both, and what each may do
```

Then backups, including scheduled ones, can never change a library, and `restore` uses the write key by itself. A computer that only makes backups (a server, a computer you reach over SSH) gets **only the read-only key** ([Over SSH](ssh.md)).

## Names and commands

Keys are stored in the system keychain under a name; the configuration only holds the name (the profile's `key`, default `zotero-backup`):

```bash
zotero-backup key set              # asks for the key (no echo), checks it with Zotero, stores it as "zotero-backup"
zotero-backup key set readonly     # …under another name; use it with "key": "readonly" in a profile
zotero-backup key check readonly   # user, libraries and rights of that key
zotero-backup key check            # every key the profile uses, and what each is for
```

A read-only key as `key` and a write key as `write_key` (see above) keep scheduled backups from ever changing a library, while `restore` uses the write key without further options:

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
