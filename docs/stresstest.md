# zotero-backup stress test

`zotero-backup stresstest` checks that a restore holds up against what people actually do in Zotero: changes on another computer, unsynced work, deletions, the trash, and Zotero's conflict window. It runs 15 operations through 5 scenarios plus a set of extras, against the real Zotero server **and a running Zotero desktop**, and writes an HTML report to read and a JSON report for further processing.

The unit tests (`go test ./...`, run in CI on Linux, macOS and Windows) use a fake Zotero server and never involve the Zotero app. They cannot show how Zotero desktop reacts when a restore changes the server under it. The stress test does exactly that.

> **The stress test changes the libraries it runs on. Run it only on test groups or libraries.** It is left out of `zotero-backup help` for that reason.

## Safety

**Recommended: a separate Zotero account just for testing.** Create a second Zotero account, sign in to it in a separate Zotero profile with the Debug Bridge plugin, and create its API key there. Its personal library and any group it owns are then truly throw-away, and no key involved can reach your real libraries at all. With your normal account, a key that may write to the personal library can write to the whole of it, because an account has only one.

- **The write key must not reach anything else.** Before asking, the test checks what the write key may do and refuses if `--library` is given and the key can write to any library not named there (the personal library or any group), listing them. Create the key at zotero.org/settings/keys with write access only to the test libraries; even a fault in the test then cannot change anything else on the server.
- **Only the libraries the write key may write to:** all of them, or those named with `--library group:<id>` or `--library user` (the key's personal library). `all` is refused.
- **A question per library, before anything changes.** After checking all libraries, it asks for each:

  ```
  This test is altering the content of the group or library.
  DO ONLY RUN IT ON A TEST GROUP OR LIBRARY!
  Are you sure that the group "Safe Edit Test" (ID 6719916, 788 items on zotero.org; Zotero data folder …) is a test group or library, and you want to run the test there? [N/y]
  ```

  The default is no. The question needs a terminal: a piped answer is refused, and there is no option to skip it.
- **Pinned to one library.** Every script it runs inside Zotero first checks the library: a group by its ID **and** name, the personal library by the signed-in user's ID **and** username, which must be the key's. It stops on any mismatch. Syncs are limited to that library.
- **Only its own objects.** Items count as the test's only if their top-level item carries the tag `zb-e2e`; collections only if their name starts with `[zb-e2e]`. Every operation refuses any other object.
- **Refuses to start** on a library if the key cannot write to it, Zotero has unsynced changes in it, it was never synced, or automatic sync is on.
- **Safety backup first.** Before anything changes in a library, a normal backup of it goes into the profile's backup folder (`to`).
- **Cleanup check at the end.** For each library it erases what it created (by its run tag, collection names and tag colours), syncs, and compares the library with its safety backup: any difference is a failed check.
- **Keys are never shown.** The API keys come from the keychain, the Debug Bridge token from the Zotero profile's `prefs.js`.

## Requirements

| Part | What | Notes |
| --- | --- | --- |
| Zotero desktop | running, signed in as the key's user | automatic sync off; the libraries under test completely synced |
| Zotero Debug Bridge | plugin in that Zotero profile, with a token set | the test runs JavaScript inside Zotero through it (`POST 127.0.0.1:23119/debug-bridge/execute`) |
| Write key | `--key NAME`, or the profile's `write_key`, else its `key` | write access to every library under test, **and to nothing else** (checked) |
| Read-only key | the profile's `key`, if it cannot write | only for the check that a read-only key is refused (skipped otherwise) |
| Zotero profile | `--zotero-profile NAME\|FOLDER` | default: the default profile in Zotero's `profiles.ini`; `ZOTERO_DEBUG_BRIDGE_TOKEN` overrides the token |

Safe Edit and Fix Proposals, if installed, are not used: neither intercepts how Zotero saves, so the test's script changes go past them, and their own changes wait for approval in a dialog.

**Conflict window.** When a sync finds an object changed on both sides, Zotero opens its conflict window and waits for a click. During the test's syncs, Zotero's function that opens it is temporarily replaced by one that answers "local" or "server" as the scenario requires, records every conflict it would have shown, and is put back right after the sync.

## Running it

`zotero-backup stresstest` without options shows these steps as help. Store the write key in the keychain, then run:

```bash
zotero-backup key set zb-test-write
zotero-backup stresstest --key zb-test-write --scenarios desktop-synced --ops edit-title,erase-item,replace-file
zotero-backup stresstest --key zb-test-write
```

No configuration file is needed: backups go to `~/zotero-backups`, the test's own backups and reports into `stresstest/<run>/` below it. A profile can hold the keys and the folder instead (`--profile test`):

```json
{
  "profiles": {
    "test": {
      "key": "zb-test-read",
      "write_key": "zb-test-write",
      "to": "~/zotero-backups"
    }
  }
}
```

`write_key` is used for all changes; `key`, if it cannot write, only for the check that a read-only key is refused (skipped otherwise); `to` is the backup folder.

| Option | Default | Meaning |
| --- | --- | --- |
| `--key NAME` | the profile's keys | keychain entry of the write key |
| `--library LIB` | every library the key may write to | only `group:<id>` or `user`; repeat for several, run in that order |
| `--scenarios A,B` | all, plus `extras` | which scenarios run |
| `--ops A,B` | all 15 | which operations run |
| `--report DIR` | the run's backup folder | where `report.html` and `report.json` go |
| `--keep` | off | keep the test objects at the end, to look at them in Zotero (the cleanup check is skipped) |
| `--zotero-profile P` | the default profile | the Zotero profile whose Debug Bridge token is used |

The run's backups go to `<to>/stresstest/<run>/`; the safety backups to `<to>` like normal backups. The command fails (exit status 1) if any check failed.

**Duration:** not measured yet. Estimated 30–45 minutes for a group like Safe Edit Test (788 items); much longer for a library with thousands of items, since every backup and every comparison reads the whole library (about 15 times per scenario).

## Fixtures and operations

Every operation acts on its own **fixture family**, created fresh in Zotero desktop for each scenario, so all operations go through one backup and restore at once without getting in each other's way. A family is:

- a book (title, date) with the tags `zb-e2e`, `zb-e2e-run-<run>`, `zb-e2e-t1` and a coloured tag `zb-e2e-colour-<run>-<op>`
- in a collection `[zb-e2e] <run> <op>` and its subcollection
- a child note, a text attachment, and a PDF attachment with a highlight annotation

Each operation exists twice: done in Zotero desktop, and done directly on the server through the API, as another Zotero client would.

| Operation | What it does | Touches |
| --- | --- | --- |
| `edit-title` | changes the title | item field |
| `fill-extra` | fills Extra, which is empty in the backup | field absent from the backup |
| `trash-item` | moves the book to the trash | trash |
| `erase-item` | erases the book with all children and files | whole family |
| `erase-note` | erases the child note | child item |
| `erase-attachment` | erases the PDF with its annotation | attachment, file, annotation |
| `erase-annotation` | erases the annotation | annotation |
| `edit-note` | adds a paragraph to the note | note |
| `replace-file` | gives the text attachment new content | file, md5 |
| `trash-collection` | moves the collection to the trash (Zotero 7) | collection trash |
| `erase-collection` | erases the collection with its subcollection | collections, memberships |
| `move-membership` | takes the book out of the collection, keeps the subcollection | collection membership |
| `tags` | removes `zb-e2e-t1`, adds `zb-e2e-added` | tags |
| `tag-colour` | changes the coloured tag's colour | library setting `tagColors` |
| `new-item` | creates an item after the backup | must be kept by restore |

## Scenarios

Each scenario runs one cycle for all operations:

1. Create the fixture families in Zotero desktop and sync them.
2. Back up the library (backup B0).
3. Make the changes as the scenario says; where desktop changes are synced, check that the server has them (and equals the desktop).
4. Restore B0.
5. Sync Zotero desktop, answering the conflict window as the scenario says.
6. Check (next section); where the scenario expects the restore to be undone, restore again, sync, check again.
7. Back up again (B1) and check that unchanged files are hard links to B0.

| Scenario | Before the restore | Conflict window answered | Expected |
| --- | --- | --- | --- |
| `desktop-synced` | change in Zotero desktop, synced | (none expected) | server and desktop back to B0, no conflict |
| `api` | change on the server by another client | (none expected) | server and desktop back to B0, no conflict |
| `unsynced-remote` | change in desktop, synced; then a second change in desktop, **not synced** | server | server and desktop back to B0 |
| `unsynced-local` | as `unsynced-remote` | local | the local version wins for the whole object and undoes the restore (recorded); a second restore and sync bring B0 back |
| `unsynced-only` | change in desktop, **not synced at all** | (none expected) | restore finds nothing to do; after the sync the server has the change; a second restore brings B0 back |

**Extras** (`--scenarios extras`), once per library on two families of their own:

- `restore --dry-run` writes nothing: the library version stays the same.
- `restore --items` restores only the selected item's family; the other stays changed.
- A read-only key is refused before anything is written.
- A second full restore right after the first finds nothing to do.
- An interrupted backup (dropped connections simulated after 2 file downloads) fails, leaves a `.partial` folder, and the next run finishes and reuses the files already downloaded.
- `prune --dry-run` with one daily backup kept lists all but the newest backup.

## Checks

After each restore and sync, every fixture family is checked on its own:

| Check | Compares | Passes when |
| --- | --- | --- |
| Backup has the fixture | B0 | all items, both collections and both files are in it |
| Server has the change | server vs B0, after the desktop's changes are synced and before the restore | something differs: the change reached the server, so the restore has something to undo |
| Server = backup | server vs B0, with the comparison `diff` uses, plus the tag colour | nothing differs (or, where the scenario expects it, something does) |
| Desktop = server | Zotero desktop vs server: fields, trash state, collections, file md5, tag colour | nothing differs |
| New items kept | the item made after the backup | still on the server, not in the trash |
| Nothing left unsynced | Zotero desktop after each sync | 0 unsynced items and collections |
| Hard links | files in B1 vs B0 | unchanged files are the same file on disk |
| Cleanup | each library vs its safety backup | identical after the fixtures are erased |

Files the server has newer than the desktop (put back by a restore) are downloaded before their md5 is compared.

## Reports

- **`report.html`**: summary (checks, passed, failed, observations), the libraries with their safety backups, the failed checks first, the scenarios, then every check.
- **`report.json`**: the same for further processing:

```json
{
  "run": "20261109T120000", "tool_version": "v0.1.0", "system": "darwin/arm64",
  "started": "…", "finished": "…", "backups": "~/zotero-backups/stresstest/20261109T120000",
  "libraries": [{"id": "group-6719916", "name": "Safe Edit Test", "safety_backup": "…"}],
  "scenarios_selected": null, "operations_selected": null,
  "summary": {"checks": 412, "passed": 410, "failed": 2, "observations": 9},
  "scenarios": [{"name": "api", "sequence": "…", "expected": "…"}],
  "results": [{"library": "group-6719916", "scenario": "api", "operation": "erase-item",
               "check": "after restore: server = backup", "result": "pass", "detail": "", "time": "…"}]
}
```

`result` is `pass`, `fail` or `info` (an observation, e.g. which conflicts Zotero's window would have shown, or that the local version won).

## Not covered

- Changes through Safe Edit or Fix Proposals: both need approval in a dialog.
- Other Zotero apps on other computers: the second client is simulated through the API.
- Group renames and very large libraries: covered by earlier real runs (IPBES LDR, 6,975 items, 3.9 GB).

## Open before the first run

Written from Zotero 10.0.7's code, not yet seen working:

- [ ] the automatic answer to the conflict window;
- [ ] `replace-file` in the desktop: marking the changed file for upload;
- [ ] a first short run with a subset, then the full run.
