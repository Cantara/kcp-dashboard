# Update knowledge.yaml content hashes

Governed skill: fix a `content_hash does not match content on disk` failure from
CI's "Validate knowledge.yaml" step (`.github/workflows/ci.yml`) after editing a file
that a `knowledge.yaml` unit tracks (currently: `README.md`, `main.go`, `view.go`,
`db.go`).

CI runs `kcp validate`, not `kcp sign --update-hashes` — deliberately, per the comment
above that step in `ci.yml`. A pipeline that silently recomputes hashes on every push
can never detect that content drifted from what was signed. Fixing the hash is a
human/agent decision, made once, on purpose — not something CI does for you.

## Preconditions
- CI (or a local `kcp validate`) reports `content_hash does not match content on
  disk` for one or more units.
- You know which source file(s) you actually intended to change (don't paper over a
  mismatch you can't explain — that means something changed content_hash wasn't
  expecting).

## Steps
1. **(read)** Open `knowledge.yaml` and find the `units:` entry whose `path:` matches
   the file(s) CI flagged (`readme` → `README.md`, `main` → `main.go`, `view` →
   `view.go`, `db` → `db.go`).
2. **(bash)** Recompute the digest for each changed file:
   ```bash
   sha256sum README.md main.go view.go db.go
   ```
3. **(edit)** For each unit whose file actually changed, replace its
   `content_hash.value` with the new digest. Leave every other unit's hash untouched —
   only edit hashes for files you changed.
4. **(bash)** Verify locally before pushing:
   ```bash
   npx --yes @cantara.no/kcp@0.29 validate ./knowledge.yaml
   ```
   (Use the explicit `./knowledge.yaml` path — a bare `knowledge.yaml` argument has
   been observed to get mis-parsed by some npx wrappers.)

## Verification
`kcp validate` prints no `content_hash does not match` error, and CI's "Validate
knowledge.yaml" job goes green.

## Rollback
Do not hand-edit `knowledge.yaml.sig` — it is regenerated automatically by
`.github/workflows/sign-kcp.yml` on any push to `main`/`master` that touches
`knowledge.yaml`, once the digests are correct. If validate still fails after step 4,
re-check you matched the right unit to the right file rather than guessing a hash.
