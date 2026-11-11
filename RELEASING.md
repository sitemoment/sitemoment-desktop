# Releasing

This file collects what a release of SiteMoment Desktop needs: the secret
checklist, the signing/notarization setup, and the trust model behind
automatic updates. Task 16 extends it with the full release runbook,
Google OAuth verification material and the per-platform signing walkthrough;
this section exists first because it documents a security property the
implementation (`internal/updates`, `cmd/updsign`, the release workflow)
already depends on.

## The update trust model

SiteMoment Desktop checks GitHub Releases for updates (`internal/updates`,
Wails' `pkg/updater`). Every release must carry a `SHA256SUMS` line for its
update asset — without one, the asset is never offered as an update. That
digest check alone only protects against corruption or truncation in
transit; it does not say the release actually came from this project's own
build. Authenticity is a second, optional layer:

- **The public key is compiled into the app** (`-X main.updatePublicKey=…`,
  set from the `UPDATE_PUBLIC_KEY` repository variable at build time — see
  `Taskfile.yml`'s `RELEASE_LDFLAGS`). It is the only trust root the running
  app has: nothing a release or its `SHA256SUMS`/`.sig` files say can change
  which key is trusted, because changing that would require a new build.
- **The private key lives only as `UPDATE_SIGNING_KEY`, a secret on a
  GitHub Actions *environment* named `release`, with required reviewers.**
  It is never a plain repository secret: an environment with required
  reviewers means a workflow run must be manually approved by one of the
  designated reviewers before that job can read the environment's secrets,
  regardless of who or what pushed the tag that triggered the run. The key
  is generated with `go run ./cmd/updsign -keygen`; nobody (including this
  agent) generates or sets it on the owner's behalf.
- **Each signature is bound to one exact release**: `cmd/updsign -sign -tag
  vX.Y.Z -name <asset-name> FILE` signs the domain-separated message
  `sitemoment-update/v1\n<tag>\n<asset name>\n<hex SHA-256>`
  (`internal/updates.SignRelease`), not just the file's bytes. That binding
  is what stops a correctly-signed old release from being replayed under a
  newer tag, or a signature made for one asset from being copied onto a
  different one — a bare digest signature would not catch either.
- **Verification happens entirely in `internal/updates`'s own provider**
  (`checked.Check`), not in Wails' updater: when a public key is compiled
  in, a release without a valid `<asset>.sig` is rejected outright (fail
  closed) before anything is downloaded. Wails' own `updater.Config` is
  never given a public key and never sees a `Signature` — only the
  digest-only `Verification` the GitHub provider already produces — so
  there is exactly one place that decides whether a release is trusted.
- **Without a compiled-in public key, updates are integrity-only, not
  authenticated.** The SHA-256 check still runs, so a truncated or
  corrupted download is still caught, but nothing ties a release to this
  project's build pipeline specifically — a compromised or
  man-in-the-middle-proxied distribution channel that can also forge
  `SHA256SUMS` would not be caught. Setting up `UPDATE_PUBLIC_KEY` /
  `UPDATE_SIGNING_KEY` is the owner's decision (see the plan's "decisions,
  revisable by the owner" list); this is not set up by default.
- **The release workflow must refuse to publish for anything but a tag
  push on this repository itself.** A fork cannot trigger a release here
  (GitHub Actions in a fork runs with the fork's own, separate secrets —
  the upstream `release` environment's `UPDATE_SIGNING_KEY` is never
  reachable from one), and the required-reviewer gate on the `release`
  environment means a tag push by itself — even by someone with write
  access — is not sufficient to produce a signed release without a
  reviewer's sign-off. The workflow should treat anything that is not an
  actual `push` of a `v*` tag on the base repository as a build-only,
  non-publishing run.

See `internal/updates/sign.go`, `internal/updates/provider.go` and
`cmd/updsign/main.go` for the implementation, and their tests
(`internal/updates/*_test.go`) for what is and is not accepted.
