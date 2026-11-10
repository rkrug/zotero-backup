# Contributing to zotero-backup

Reports, tests on other systems and pull requests are welcome. zotero-backup is a one-person project, so a short issue first is the best start for anything larger than a fix.

## Reports

- **A problem:** open a [bug report](https://github.com/rkrug/zotero-backup/issues/new?template=bug_report.md). It asks for the output of `zotero-backup version`, the system, the command, what happened and what you expected, and `problems` / `missing_files` from the backup's `meta.json` when a backup is involved.
- **Never put an API key into an issue**, a log or a screenshot, and no private library content (titles, notes, file names) you do not want public. Shorten or replace what is not needed to show the problem.
- **A security problem** (keys, restores writing where they should not): not as an issue, see [SECURITY.md](SECURITY.md).

## Tests on Windows and Linux

zotero-backup is tested by hand only on macOS so far. Windows ([#15](https://github.com/rkrug/zotero-backup/issues/15)) and Linux ([#16](https://github.com/rkrug/zotero-backup/issues/16)) have checklists; a good report, with the [test report template](https://github.com/rkrug/zotero-backup/issues/new?template=test_report.md), names:

- the system and version (e.g. Windows 11 23H2, Ubuntu 24.04 with GNOME), and the zotero-backup version;
- which checks from the list you ran, and for each: passed, or what happened instead (the exact message);
- for the stress test: the summary line and `report.json` (it contains library names and IDs, but no keys).

## Pull requests

- **Into `dev`**, not `main`: `main` is protected and receives only the release pull requests; releases are tagged there.
- **Go's standard library only**, no dependencies.
- **The checks CI runs** (Linux, macOS, Windows), to run before pushing:

    ```bash
    gofmt -l .           # prints nothing
    go vet ./...
    go test -race ./...
    ```

  Tests use fake servers (`httptest`); no test talks to the real Zotero API.
- **Documentation** for users is in [`docs/`](docs/) (MkDocs, Material theme); update it with the change. Preview it with

    ```bash
    pip install mkdocs-material==9.6.14
    mkdocs serve          # http://127.0.0.1:8000
    ```

  CI builds it with `mkdocs build --strict`: broken links fail.
- Add a line to the "Unreleased" section of [CHANGELOG.md](CHANGELOG.md) for anything users notice.
- `CLAUDE.md` describes the structure of the code and the Zotero API facts the design depends on.

## The stress test

`zotero-backup stresstest` runs end to end against the real Zotero API and a running Zotero desktop, and **changes the libraries it runs on**. Run it only on throw-away test libraries, with a write key that may write to nothing else (it refuses other keys), ideally in a separate Zotero account just for testing. Read the [stress test documentation](https://rkrug.github.io/zotero-backup/stresstest/) first.
