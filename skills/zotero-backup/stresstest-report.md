# zotero-backup: reading a stress test report

For a user who ran `zotero-backup stresstest` themselves and shares the result. Never run the stress test yourself (SKILL.md, rule 5). Details: the documentation's Stress test page (scenarios, operations, checks).

## Where the results are

- The run ends with `N checks: P passed, F failed, O observations` and `report: …/report.html, …/report.json`; the command fails (exit status 1) if any check failed.
- `report.json`: `run` (the run ID, e.g. `20261109T154249`), `tool_version`, `system`, `libraries` (ID, name, the safety backup made before the run), `summary` (`checks`, `passed`, `failed`, `observations`), `scenarios` (each with its sequence and what is expected), and `results`: one record per check with `library`, `scenario`, `operation`, `check`, `result` (`pass`, `fail` or `info`) and `detail`.
- `report.html` shows the same: summary, failed checks first, then everything.

## Reading it

- **Start with the failed checks**, grouped by `scenario` and `operation`; many failures with one operation or one scenario usually have one cause. `detail` says what differed (object keys, fields, md5s).
- **`info` is an observation, not a failure**: e.g. `conflicts` (which objects Zotero's conflict window showed) or `local version won` in the scenario `unsynced-local`, where that is expected.
- **Scenarios** (`desktop-synced`, `api`, `unsynced-remote`, `unsynced-local`, `unsynced-only`, `extras`) say how the change was made before the restore; **operations** (`edit-title`, `erase-item`, `replace-file`, `trash-collection`, `tag-colour`, `zip`, …) what was changed; `*` is a check for the whole scenario.
- **Check names**, in order of a cycle:
  - `backup has the fixture …`: the backup before the change is complete;
  - `before restore: server has the change`: the change reached the server; if this fails, the restore had nothing to undo (often: Zotero desktop did not sync);
  - `after restore: server = backup`: the restore worked on the server;
  - `after restore: desktop = server`: Zotero desktop took the restore over after its sync;
  - `item created after the backup is kept`: restore deleted nothing new;
  - `second change to K (not changed by the restore) uploaded`: an unsynced change on an object the restore did not touch stayed (expected);
  - `file K hard-linked in the next backup`: unchanged files are not stored twice;
  - `after the first / second restore`: scenarios that expect the first restore to be undone (local version won) and restore again.
- **Usual causes of failures** that are not zotero-backup's: automatic sync switched on in Zotero desktop during the run, Zotero closed or the Debug Bridge plugin not running during the run, the network dropped, another Zotero app syncing the test library at the same time. Ask the user about these before calling it a bug.

## After a failed or stopped run

- Test objects (tag `zb-e2e`) left in the test libraries: the user runs `zotero-backup stresstest --cleanup RUN --key WRITEKEY` (RUN from `report.json`) in their own terminal.
- A failure that is not explained by the causes above: suggest a bug report with the failed records from `report.json` (it has library names and IDs, no keys) and `tool_version` / `system`.
