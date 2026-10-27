# GHCR image retention

The Docker workflow maintains only `ghcr.io/guaizai233/frostagent`:

- Keep every version-number image, such as `v1.0.1`. Numeric `1.0.1` tags and
  prerelease/build suffixes such as `v1.1.0-rc.1` are also protected.
- Keep the current `main` image. `main` and `latest` must resolve to the same
  digest; releases never move `latest`.
- Keep the 10 most recent SHA-tagged intermediate commit images in addition to
  the current `main` / `latest` image and all version-number releases. The
  current main image and release-tagged images do not consume this allowance.
- Delete older SHA-only commit images and unreferenced, untagged manifests.
  New builds publish `sha-*` tags so recent commits remain directly pullable.
- Keep the complete manifest graph of protected and retained images, including
  nested multi-platform indexes, shared platform manifests and embedded BuildKit
  provenance attestations. These dependencies may appear untagged in GitHub;
  deleting them would break the retained images. This workflow does not publish
  separate referrer-based signatures or attestations.

GitHub deletes **package versions**, not individual tag aliases. A historical
`sha-*` tag sharing a digest with a protected release, main image or dependency
remains on that retained version. It is not an additional historical image.
The ten-image limit applies to commit-image roots, not the manifests they depend
on; older manifests required by a retained image are kept. Older, unreferenced
commit digests outside the ten-image window are deleted.

## When cleanup runs

- After each successful image publication, within the publishing job.
- Daily at 03:23 UTC (11:23 Asia/Shanghai), as a catch-up sweep.
- Manually through **Actions → Docker → Run workflow**, on `main`.
  `dry_run` defaults to **true** and only lists deletion candidates. Set it to
  **false** to delete them. Schedule/manual events do not rebuild the image.

Publish and cleanup jobs share one non-cancelling concurrency group. A queued,
superseded main build is skipped before publication so it cannot roll `latest`
back after a newer build has published. All future publishers for this package
must use the same lock; do not push/retag manually while cleanup is running.
Both jobs use `queue: max` so cleanup cannot replace a pending release publish.
GitHub allows up to 100 pending jobs in this queue; overflowing jobs are rejected.
See [GitHub's concurrency documentation](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency).

The script reads every Packages API page and every paginated GHCR `tags/list`
page, then requires both sources to report exactly the same tag-name set and
tag-to-digest mappings before planning deletion. Every tag is resolved through
GHCR, including SHA tags outside the ten-image window and other aliases on
deletion candidates. It walks the manifest references of retained images.
Tag-name or digest mismatches, divergent main aliases, unknown manifests,
incomplete responses and permission errors fail closed. Before applying, it
rechecks the package snapshot and freshly resolves the entire registry tag map
again, then revalidates each candidate immediately before deletion. HTTP failures
stop the job visibly; a partial deletion is safe to retry. GitHub APIs do not provide a
transaction spanning registry pushes and package deletion, so the shared lock
is required, not just these consistency checks.

## Permissions and recovery

The jobs use `GITHUB_TOKEN` with `contents: read` and `packages: write`, not a
stored personal token. The repository must have **Admin** access under the
package's **Settings → Manage Actions access** to delete package versions.
GitHub normally grants this when the repository's workflow publishes the
package. A 403 must be fixed there; the script never silently ignores it.
Publishing and cleanup are restricted to the upstream repository, not forks.

GitHub may refuse to delete public package versions with more than 5,000
downloads. Deleted versions can normally be restored within 30 days while the
namespace/version is available. See GitHub's
[deletion and restoration documentation](https://docs.github.com/en/packages/learn-github-packages/deleting-and-restoring-a-package).

## Local verification

```sh
python3 -m unittest discover -s scripts -p 'test_ghcr_cleanup.py' -v
python3 -m unittest discover -s scripts -p 'test_dependency_pins.py' -v
```

These synthetic tests do not contact GitHub or delete images. For an authenticated
preview, set `GH_TOKEN` and `GITHUB_ACTOR` and run
`python3 scripts/ghcr_cleanup.py` (read-only). Local credentials need package read
access; an authenticated `gh` session alone does not imply this scope exists.
Only `--apply` enables deletion. Prefer the workflow's preview so package access
and the shared concurrency lock match production.
