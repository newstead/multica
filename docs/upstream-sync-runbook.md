# Upstream Sync Runbook

Internal ops runbook for syncing the `newstead/multica` fork with the official
`multica-ai/multica` upstream. Written for future upstream-sync cycles.

- Fork: `https://github.com/newstead/multica`
- Upstream: `https://github.com/multica-ai/multica`
- Cycle example (v0.4.16): integration branch `upstream-sync/v0.4.16-production`,
  release tag `v0.4.16-ns.1`.

## 1. Branch policy

| Ref | Role |
|---|---|
| `production` | Canonical prod branch, created from the prod baseline commit (e.g. `b92b3be51` / `v0.4.14-ns.11`). Final release merges land here. |
| `upstream-sync/<ver>-production` | Integration branch per cycle, created from `origin/production` at cycle start. PR target for all cycle work. |
| `agent/*` | Per-task feature branches, branched from `origin/upstream-sync/<ver>-production`. |
| `main`, `mega`, bare `multmemory` | Never used by this project. Do not branch from or target them. |

Rules:

- Always fetch first: `git fetch origin --prune` and `git fetch upstream --prune --no-tags`.
  The `remote.upstream.tagOpt` config from §2 is mandatory, not optional.
- Feature branches start from `origin/upstream-sync/<ver>-production`.
- Every implementation PR targets `upstream-sync/<ver>-production` — never `main`,
  `mega`, or bare `multmemory`.
- Merge into `production`, tagging, and deploy are release-gated (human approval).

## 2. Fetch and compare an upstream release

```bash
# one-time remote setup
git remote add upstream https://github.com/multica-ai/multica
git config remote.upstream.tagOpt --no-tags   # MANDATORY: no upstream fetch ever auto-follows bare tags

# per cycle
git fetch origin --prune
git fetch upstream --prune --no-tags   # no auto-follow of bare upstream tags, see §3
git fetch --no-tags upstream refs/tags/v0.4.16:refs/tags/upstream/v0.4.16   # prefixed tag, see §3

# release commit
git rev-parse upstream/v0.4.16^{}
git log -1 --format='%H %s' upstream/v0.4.16

# divergence between the prod baseline and the release
git merge-base origin/production upstream/v0.4.16
git log --oneline --left-right origin/production...upstream/v0.4.16
git diff --stat origin/production upstream/v0.4.16

# changelog / release notes
git log --oneline --no-merges origin/production..upstream/v0.4.16

# migration inventory (see §5)
git ls-tree --name-only upstream/v0.4.16:server/migrations | grep -E '^[0-9]+_.*\.up\.sql$' | sort
```

Note: `git ls-tree --name-only <ref> server/migrations/` prints full paths
(`server/migrations/250_...`), so the grep would not match. Use the `ref:path`
tree syntax (or strip the `server/migrations/` prefix first).

## 3. Safe upstream tag handling

- Never push bare upstream tags (`v0.4.16`) to `origin` — our releases use
  `-ns` tags only.
- `git config remote.upstream.tagOpt --no-tags` is mandatory on every clone
  (see §2). Tag auto-follow applies to explicit refspec fetches too — a plain
  `git fetch upstream refs/tags/v0.4.16:refs/tags/upstream/v0.4.16` would
  import every reachable bare tag (observed: 138 tags, including bare
  `v0.4.16`). The config, plus `--no-tags` on each explicit fetch, guarantees
  only the `upstream/` prefixed tag is imported.
- Import upstream tags under the `upstream/` prefix so they cannot be confused
  with ours: `git fetch --no-tags upstream refs/tags/v0.4.16:refs/tags/upstream/v0.4.16`.
  A plain `git fetch upstream --tags` can clobber locally cached tags; if it
  reports `would clobber existing tag`, verify the local copy matches the
  upstream commit instead of force-overwriting. If a bare tag was already
  auto-followed in the past and exists locally, verify it points at the same
  upstream commit and never push it to `origin` (optionally delete the local
  copy: `git tag -d v0.4.16`).
- Local prod tags always carry the `-ns` suffix: `v0.4.16-ns.1`. Never tag our
  releases with a bare `v0.4.16`.
- Sanity check before release:

```bash
git tag -l 'upstream/v0.4.16*'   # imported upstream tag present
git tag -l 'v0.4.16-ns.*'        # our release tags only
git push origin v0.4.16-ns.1     # push only -ns tags
```

  A bare glob like `v0.4.16*` cannot match the `upstream/` prefix, so check the
  two namespaces separately.

- The release workflow (`.github/workflows/release.yml`) triggers on `v*.*.*`
  tags and validates semver (refuses `*-dirty*`). On the fork it builds and
  pushes `ghcr.io/newstead/multica-backend` and `ghcr.io/newstead/multica-web`;
  the GoReleaser/brew and helm-chart jobs are skipped (guarded by
  `github.repository_owner == 'multica-ai'`).

## 4. git rerere for long-running fork conflicts

The fork replays the same conflicts on every upstream sync. Enable rerere so
recorded resolutions are applied automatically on later merges:

```bash
git config --global rerere.enabled true
git config --global rerere.autoUpdate true
```

- During conflict resolution, resolve and `git add` normally; rerere records the
  resolution keyed by the conflict hunk.
- On later merges rerere auto-applies known resolutions — always review with
  `git rerere diff` before committing.
- Useful commands: `git rerere status`, `git rerere diff`, `git rerere forget <path>`,
  `git rerere gc`.
- Rerere state lives in `.git/rr-cache`, so prefer a persistent integration
  clone across cycles (or record the resolutions as a documented patch and
  re-apply it).

## 5. Migration prefix collision check

- Upstream occupies a numeric prefix range per release (v0.4.16 ends at `250`).
  Our fork migrations previously used `235..246`; after integration they were
  renumbered to start after the upstream max (`251..264`).
- Check the max prefix on each side before/after the merge:

```bash
git ls-tree --name-only origin/upstream-sync/v0.4.16-production:server/migrations | grep -E '^[0-9]+_.*\.up\.sql$' | sed -E 's/^([0-9]+)_.*/\1/' | sort -n | tail -1
git ls-tree --name-only upstream/v0.4.16:server/migrations | grep -E '^[0-9]+_.*\.up\.sql$' | sed -E 's/^([0-9]+)_.*/\1/' | sort -n | tail -1
```

  Use the `ref:path` tree syntax here too — the `server/migrations/` form
  returns full paths and the grep will match nothing.

- Rules:
  - Carried local migrations start after the upstream max prefix (v0.4.16 → `251`).
  - Prod-applied local migrations must stay idempotent — the prod DB already
    has the objects from the previous release.
  - Never reuse a prefix present on either side; keep `.up.sql`/`.down.sql` pairs.
- Prefix uniqueness inside the repo is enforced by
  `server/internal/migrations/migrations_lint_test.go`
  (unique numeric prefixes after the frozen legacy range ≤ `148`).
  Run it with `cd server && go test ./internal/migrations/`.

## 6. Project / task / PR target rules for agents

- Work only on `production` / `upstream-sync` branches; fetch `origin` and
  `upstream` first.
- Feature branch from `origin/upstream-sync/<ver>-production`; PR target is
  always `upstream-sync/<ver>-production`.
- Preserve prod features: MultMemory (memory gateway, mem0/Hindsight boards),
  DeepSeek runtime, Kimi cost tracking, language policy, usage telemetry, inbox
  project context, selfhost/release changes.
- PR evidence: merge base, divergence, conflict summary, unresolved risk list,
  verification commands.
- Handoff chain: `upsync-dev` → `upsync-qa` → `upsync-lead` / `upsync-release`.
  Dev never merges, tags, deploys, restarts, or changes credentials.
- Comments, handoffs and blocker reports in Russian (except commands, paths,
  branch names, commit ids, model/status ids, code quotes).

## 7. Release / deploy checklist and rollback

### Pre-release gate (human approval required)

- QA passed on the integration branch (upsync-qa report).
- Migrations reconciled: no prefix collision with upstream, carried migrations
  idempotent, migration lint tests green.
- Integration branch merged into `production` (prefer fast-forward or a clean
  merge that preserves prod features).

### Release steps

1. Tag from `production`: `git tag v0.4.16-ns.1 production` — never a bare `v0.4.16`.
2. Push the tag: `git push origin v0.4.16-ns.1`.
3. Confirm the release workflow builds `ghcr.io/newstead/multica-backend` and
   `ghcr.io/newstead/multica-web` for the tag (`.github/workflows/release.yml`).
4. Deploy per the selfhost procedure — pin the fork images and the release tag
   in `.env`. The compose defaults (`ghcr.io/multica-ai/...`) do not receive
   the fork's images, so all three must be set:

```bash
MULTICA_BACKEND_IMAGE=ghcr.io/newstead/multica-backend
MULTICA_WEB_IMAGE=ghcr.io/newstead/multica-web
MULTICA_IMAGE_TAG=v0.4.16-ns.1
```

   then:

```bash
docker compose -f docker-compose.selfhost.yml pull
docker compose -f docker-compose.selfhost.yml up -d
```

   Helm variant: `helm upgrade multica oci://ghcr.io/multica-ai/charts/multica --version <chart-version> -n multica -f my-values.yaml`.

5. Verify: health endpoint OK, migrations applied (backend runs `migrate up` on
   startup), key prod features alive (memory, language policy, telemetry).

### Rollback

- Previous known-good: `v0.4.14-ns.11` / `b92b3be51`.
- Compose: restore the same three `.env` values — keep
  `MULTICA_BACKEND_IMAGE=ghcr.io/newstead/multica-backend` and
  `MULTICA_WEB_IMAGE=ghcr.io/newstead/multica-web`, set
  `MULTICA_IMAGE_TAG=v0.4.14-ns.11` — then `pull` and `up -d`.
- Helm: `helm -n multica rollback multica` (rolls back to the previous revision).
- DB: migrations run forward on startup; downgrade requires the `.down.sql` for
  the delta or a restore from backup — take a DB backup before deploying.
  Do not drop tables blindly.
- Escalate immediately if rollback is needed; do not silently re-deploy.
