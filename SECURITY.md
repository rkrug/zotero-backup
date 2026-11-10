# Security

## Reporting a problem

Report security problems **privately**, not as a public issue: on the repository's [Security tab](https://github.com/rkrug/zotero-backup/security), **Report a vulnerability** (GitHub's private vulnerability reporting). Only the maintainer sees the report; a fix and an advisory follow before the details are made public.

**Never send an API key**, not even a revoked one, and no private library content. If a key may have leaked, delete it at <https://www.zotero.org/settings/keys> first and create a new one.

## In scope

- **API keys:** how zotero-backup reads, stores and passes them (system keychains on macOS, Windows and Linux; `ZOTERO_API_KEY`); a key that ends up in output, logs, files, backups or command lines.
- **The key file for scheduled runs** described in the [documentation](https://rkrug.github.io/zotero-backup/scheduled/) (its permissions and how it is read).
- **Restores:** `restore` writing into a library other than the backup's, or changing more than its plan shows.
- **Backups and prune:** files written or deleted outside the backup folder (e.g. through names from the library).
- **Release archives:** a mismatch with `SHA256SUMS`.

Not in scope: the security of the Zotero servers, Zotero desktop or the systems' keychains themselves; report those to their makers.

## Supported versions

Only the newest release gets fixes.
