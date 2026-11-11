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

## Versioning

- Tags look like `v<major>.<minor>.<patch>`, e.g. `v0.5.0`; a pre-release
  adds a suffix, e.g. `v0.5.0-rc.1`.
- A release without a suffix becomes GitHub's latest release and clients
  update to it automatically; one with a suffix becomes a prerelease -
  **clients never auto-update to it**, it is only for downloading by hand
  (use it to test a release on yourself first).
- The version embedded in the build files (`build/config.yml`, `Info.plist`,
  the Windows version resource, manifest and NSIS) is written at build time
  by `go run ./cmd/stamp-version <version>`; **never hand-edit those files or
  commit a stamped copy**. The version compiled into the binary comes from
  `-ldflags -X main.version=…` (`Taskfile.yml`'s `RELEASE_LDFLAGS`).
- Only ever release forward: the updater only accepts a strictly higher
  version. Published the wrong thing? Release a higher patch version; do not
  try to replace or delete a release that a client may already have checked
  (`gh release delete` only removes it from the download page, not from any
  update check a client already completed).

## Releasing a version

1. `main`'s CI (`.github/workflows/ci.yml`) is green; README's "当前状态"
   describes this release's changes.
2. Tag and push (**only when the owner decides to release**):

   ```bash
   git checkout main && git pull --ff-only
   git tag -a v0.5.0 -m "SiteMoment Desktop 0.5.0"
   git push origin v0.5.0
   gh run watch -R sitemoment/sitemoment-desktop    # watch the Release workflow
   ```

3. The workflow (`.github/workflows/release.yml`): runs CI → requires
   `UPDATE_PUBLIC_KEY` to be set (fails the `version` job immediately
   otherwise) → builds the macOS universal binary (arm64 + amd64), signs and
   notarizes it when Apple credentials exist, packages the `.dmg` and the
   self-update `.zip` → builds the Windows exe, signs it when a Windows
   certificate exists, packages the per-user NSIS installer (with the
   WebView2 bootstrapper) and the self-update `.zip` → in the one job bound
   to the `release` environment (requires a reviewer's approval, since that
   environment holds `UPDATE_SIGNING_KEY`): signs every update asset with
   `cmd/updsign`, writes `SHA256SUMS`, writes the release notes (commits
   since the previous tag) → publishes the GitHub release.
4. Verify after release:

   ```bash
   gh release view v0.5.0 -R sitemoment/sitemoment-desktop
   cd /tmp && rm -rf smd-rel && mkdir smd-rel && cd smd-rel
   gh release download v0.5.0 -R sitemoment/sitemoment-desktop
   shasum -a 256 -c SHA256SUMS
   ```

   The download page `https://sitemoment.com/seo/download`'s two buttons
   point at `releases/latest/download/<file>`, which take effect immediately
   on release.

| File | Purpose |
|---|---|
| `SiteMoment-macos-universal.dmg` | macOS install (drag into "应用程序") |
| `SiteMoment-macos-universal.zip` | macOS self-update (`SiteMoment.app` is its only entry) |
| `SiteMoment-windows-amd64-setup.exe` | Windows install (per-user, `%LOCALAPPDATA%\Programs\SiteMoment`) |
| `SiteMoment-windows-amd64.zip` | Windows self-update (`sitemoment-desktop.exe` is its only entry) |
| `*.zip.sig` | Ed25519 signature of the matching update zip, bound to this exact tag and asset name |
| `SHA256SUMS` | SHA-256 of every asset above; the client checks it before updating |

File names carry no version, so `releases/latest/download/…` always points
at the newest release; **do not rename them** (`internal/updates/assets.go`
and the download page both depend on these exact names).

If something goes wrong: `gh release delete v0.5.0 --cleanup-tag -R
sitemoment/sitemoment-desktop` removes the release and the tag; fix the
problem and release a higher version. You can also re-run Release by hand on
an existing tag from the Actions tab (fill in the tag input).

**Test build (nothing published)**: running Release by hand without a tag
builds `main`'s latest commit as version `0.0.0-ci.<run number>`, through
both platforms' build steps (signing included, when the relevant secrets
exist), but never touches the `release` environment or `UPDATE_SIGNING_KEY`
and never creates a GitHub release - the artifacts only attach to the run:

```bash
gh workflow run release.yml -R sitemoment/sitemoment-desktop --ref main
gh run watch -R sitemoment/sitemoment-desktop
gh run download <run id> -R sitemoment/sitemoment-desktop -D /tmp/smd-ci    # macos/ and windows/ directories
```

## Secrets and variables (all optional except the update-signing pair; add them under repository Settings → Secrets and variables → Actions as needed)

`UPDATE_PUBLIC_KEY` and `UPDATE_SIGNING_KEY` are **required for any real
release** (a tag push): the `version` job fails immediately without
`UPDATE_PUBLIC_KEY`, and the `publish` job fails without
`UPDATE_SIGNING_KEY` once a reviewer approves the `release` environment (see
"The update trust model" above). Everything else is genuinely optional -
without the signing-related secrets, the release still ships, just
unsigned; the release notes say how to open it on each platform.

| Name | Kind | Purpose |
|---|---|---|
| `UPDATE_PUBLIC_KEY` | repository variable | The update-signing public key (base64); compiled into the client. **Required** for a real release. |
| `UPDATE_SIGNING_KEY` | secret on the `release` **environment** (with required reviewers) | The update-signing private key (base64, a 32-byte seed). **Required** for a real release; never a plain repository secret. |
| `GOOGLE_CLIENT_ID` | repository variable | Google OAuth "desktop app" client ID. Without it, the released client cannot reach Google |
| `GOOGLE_CLIENT_SECRET` | repository secret | That client's secret (Google itself documents that a desktop client's secret is not confidential, but it still is not committed to the repository) |
| `MAC_CERT_P12_BASE64` | repository secret | The "Developer ID Application" certificate and its key, exported as a .p12, base64 |
| `MAC_CERT_PASSWORD` | repository secret | The .p12's password |
| `MAC_SIGN_IDENTITY` | repository secret | The certificate's name, e.g. `Developer ID Application: Example Ltd (ABCDE12345)` |
| `MAC_NOTARY_KEY_P8_BASE64` | repository secret | An App Store Connect API key (.p8), base64; used for notarization |
| `MAC_NOTARY_KEY_ID` | repository secret | That API key's Key ID |
| `MAC_NOTARY_ISSUER_ID` | repository secret | Its Issuer ID |
| `WINDOWS_CERT_PFX_BASE64` | repository secret | The Authenticode code signing certificate, .pfx, base64 |
| `WINDOWS_CERT_PASSWORD` | repository secret | The .pfx's password |
| `WINDOWS_TIMESTAMP_URL` | repository variable | RFC 3161 timestamp server, default `http://timestamp.digicert.com` |

Setting them (run inside this repository's directory):

```bash
gh variable set GOOGLE_CLIENT_ID --body "<id>.apps.googleusercontent.com"
gh secret set GOOGLE_CLIENT_SECRET            # paste then Enter, Ctrl-D
base64 -i DeveloperID.p12 | gh secret set MAC_CERT_P12_BASE64
```

The `release` environment itself (required reviewers, and the place
`UPDATE_SIGNING_KEY` lives) is created once under repository Settings →
Environments; this agent does not create it or set any of the secrets above
on the owner's behalf (see AGENTS.md and the plan's Task 18).

### The update signing key pair (free, add it whenever)

```bash
go run ./cmd/updsign -keygen     # prints UPDATE_PUBLIC_KEY=… and UPDATE_SIGNING_KEY=…
```

Set the public half as the repository variable `UPDATE_PUBLIC_KEY` and the
private half as the secret `UPDATE_SIGNING_KEY` **on the `release`
environment** (not a plain repository secret); back the private key up
offline too. `cmd/updsign -sign` needs the exact tag and asset name it is
signing for:

```bash
UPDATE_SIGNING_KEY=… go run ./cmd/updsign -sign -tag v0.5.0 -name SiteMoment-macos-universal.zip SiteMoment-macos-universal.zip
go run ./cmd/updsign -verify -pub "$UPDATE_PUBLIC_KEY" -tag v0.5.0 -name SiteMoment-macos-universal.zip SiteMoment-macos-universal.zip
```

**Where the current key pair lives** (set up 2026-11-11, Task 18): the
public half is the repository variable `UPDATE_PUBLIC_KEY`; the private
half is the secret `UPDATE_SIGNING_KEY` on the `release` **environment**
(required reviewer: `allenyoda`; deployment branch policy limited to tags
matching `v*`). It is also backed up offline, in this Mac's login keychain,
as a generic password item — service `SiteMoment update signing key`,
account `sitemoment-desktop` (`security find-generic-password -a
sitemoment-desktop -s "SiteMoment update signing key" -w` reads it back).
No copy of the private key lives in this repository or in any log.

Rotating the key: sign one more release under the old private key that
compiles in the new public key, then switch to the new private key from the
next release on. **If the private key is lost**, clients that already have
the old public key compiled in will refuse every future release until
reinstalled from the download page - keep the backup.

### macOS signing and notarization

1. Create a "Developer ID Application" certificate in the Apple Developer
   Program account, and export it together with its private key as a .p12
   (set a password) from Keychain Access. `security find-identity -v -p
   codesigning` lists the certificate's name. This Mac currently has a
   `Developer ID Application: Heartie Technology Limited (X3FLP9863V)`
   certificate: using it means users see Heartie Technology Limited as the
   developer, which is the owner's call, not this agent's - it is not used
   by anything this agent built.
2. App Store Connect → Users and Access → Integrations → Team Keys, create a
   new API key with the "Developer" role, download the .p8 (only
   downloadable once), note its Key ID and Issuer ID.
3. Add the six `MAC_*` secrets above. On the next release,
   `build/darwin/sign.sh` signs `SiteMoment.app` with hardened runtime,
   `build/darwin/entitlements.plist` and a secure timestamp, submits it for
   notarization, waits, and staples the ticket; the `.dmg` is signed,
   notarized and stapled too. A rejected notarization fails the workflow;
   the log includes `notarytool log`'s output.

Local test signing (only checks that the hardened-runtime signature itself
works; does not notarize, does not touch the network):
`MAC_SIGN_IDENTITY=- build/darwin/sign.sh app bin/SiteMoment.app`, then open
it. **Never use the real Developer ID certificate in this Mac's keychain for
a local build** - only ever `MAC_SIGN_IDENTITY=-` (ad-hoc) locally; a
Developer-ID-signed, notarized release only ever happens through the
`release` environment in CI.

### Windows signing

`build/windows/sign.ps1` uses `signtool` and a .pfx to sign
`sitemoment-desktop.exe` and the installer (SHA-256 digest + RFC 3161
timestamp). Note: since June 2023, newly issued OV/EV code signing
certificates must keep their private key in hardware (a USB token, an HSM,
or a CA's cloud signing service), so an exportable .pfx is usually no longer
available. If that is the case when a certificate is bought, use the CA's
cloud signing tool or Azure Artifact Signing (formerly Trusted Signing)
instead, replacing the `signtool sign /f …` line in `sign.ps1`; the rest of
the flow is unchanged. The uninstaller (`uninstall.exe`) is not signed at
all currently.

## Local packaging (unsigned/ad-hoc only)

Local builds exist to check that packaging works, never to produce a
release someone installs: **never use the Developer ID certificate in this
Mac's keychain for a local build** - only ad-hoc (`MAC_SIGN_IDENTITY=-` or
unset) signing locally. Real signed, notarized, published builds only ever
happen in CI, through the `release` environment.

```bash
go run ./cmd/stamp-version 0.5.0          # stamps a few files under build/
VERSION=0.5.0 wails3 task darwin:release  # bin/release/SiteMoment-macos-universal.{dmg,zip}
git checkout -- build/                     # revert the stamped files
```

The Windows installer needs `makensis`, only built on Windows (or in CI); on
a Mac you can cross-compile the exe to check that it builds: `VERSION=0.5.0
wails3 task windows:build ARCH=amd64`.

## Google OAuth app verification (before public download)

Search Console's and Analytics' read-only scopes are both sensitive scopes;
until verification is approved, at most 100 test users. In Google Cloud
Console's "OAuth consent screen / Google Auth Platform → Branding":

- App name `SiteMoment`; app logo shrunk from `build/appicon.png` to 120×120
  (`sips -z 120 120 build/appicon.png --out /tmp/sitemoment-logo-120.png`).
- App home page `https://sitemoment.com/seo/download`; privacy policy
  `https://sitemoment.com/seo/privacy`; terms of service
  `https://sitemoment.com/terms`; authorized domain `sitemoment.com`
  (ownership verified in Search Console).
- Data access: `openid`, `email`, `…/auth/webmasters.readonly`,
  `…/auth/analytics.readonly`; the usage justification should say clearly
  that the user's own website data is shown and analyzed on the user's own
  computer, and stored locally.
- Demo video (an unlisted YouTube link): install → connect a Google account
  → the consent screen shows all four scopes → pick a Search Console
  property and a GA4 property → the overview shows numbers → ask the Agent
  a question.
- The privacy policy page already includes the Google API Services User
  Data Policy's Limited Use disclosure.
