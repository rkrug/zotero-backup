# Keys

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
