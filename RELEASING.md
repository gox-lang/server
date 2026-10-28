# Releasing GOX

GitHub Releases is the only source of GOX executables. Each distribution packages
the same versioned, checksum-verified executable; no channel rebuilds GOX.
Package managers retain immutable versions. Users can install without specifying
a version, while maintainers publish a stable server release once.

## One-time configuration

1. In the server repository, create the Actions secret `RELEASE_CHANNELS_TOKEN`.
   Use a fine-grained token owned by `gox-lang`, limited to `npm`,
   `homebrew-tap`, and `linux-packages`, with **Actions: read and write** and
   the automatically granted metadata access. This token only dispatches and
   monitors workflows. Track its expiry and replace it before expiration.
   Docker uses the server workflow's own `GITHUB_TOKEN`.
2. On npm, configure a GitHub Actions **trusted publisher** for `@gox-lang/cli`:
   organization `gox-lang`, repository `npm`, workflow `release.yml`,
   environment `npm-publishing`. No npm access token is stored in GitHub.
   Create that GitHub environment and restrict deployments to `main`.
3. Keep the Linux `package-signing` environment and its existing
   `GOX_SIGNING_KEY`. Keep GitHub Pages configured for Actions and
   `packages.gox-lang.dev`. Do not rotate the signing key as part of a release.
4. Channel workflows require permission to push their small version changes to
   their own `main` branches. If rulesets prohibit these pushes, configure an
   approved automation identity or switch to a reviewed PR publication flow.
5. Upload the release-info action in server and all channel workflows before
   publishing a release. Channel workflows intentionally trust the shared action
   on the official server's `main` branch, allowing orchestration fixes without
   changing already published binary tags.

## Publish

1. Run tests and review the release diff. SDK and RedisAdapter requirements must
   match, including the SDK version embedded in server build information.
   Run `TestSDKVersionCompatibility` with `GOX_REDIS_ADAPTER_DIR` set, and adapter
   tests with an isolated Redis and `GOX_REDIS_TEST_ADDR`.
2. Commit and push to `main`; wait for CI. Create and push a new stable tag such as
   `v0.1.4`. Never move a published tag.
3. **Release binaries** verifies SDK/server/adapter compatibility, builds six
   native archives, runs binary smoke checks, and creates a draft release with
   `checksums.sha256`.
4. Review its notes and publish the draft as the latest stable release.
   Publishing through the GitHub UI triggers **Distribute stable release**.
   If a future workflow publishes with `GITHUB_TOKEN`, it must explicitly
   dispatch `distribute.yml`: GitHub suppresses most events created by that token.
5. Open the distribution run. It validates the published release and complete
   manifest, dispatches each channel, and waits for the actual channel result.
   A dispatched job is not considered a successful publication.

## Channels

- **npm:** prepare the matching package version, test installation on six native
  platforms, publish using OIDC, verify anonymous installation, and commit
  package/lock version changes. Existing versions must have identical tarball
  integrity; other registry errors are failures, not evidence of a missing version.
- **Homebrew:** generate four archive URLs and checksums, test all four platforms,
  then commit the tested formula. No manual hash or version edits.
- **Docker:** build and smoke-test amd64/arm64 images from release binaries, publish
  the versioned manifest, and update `latest` only for the current stable release.
- **APT/RPM:** build/test both architectures, retain approved historical packages,
  sign and validate the complete snapshot, publish to Pages, verify public signed
  installation, and record the new revision-1 release. Test revision 2 is excluded.
- **curl, direct downloads, GOX Tools, and gox update:** already resolve GitHub's
  latest stable release. No extension release is needed for a server-only fix.
- **go install:** resolves the Go module tag; the Go proxy may take time to observe
  a new version. Use the exact tag to verify it, then check `@latest`.

Publication is not atomic across registries. Existing installed versions and
running applications are not automatically restarted. Wait for all channels to
pass before announcing that distribution is complete.

## Retry and failure handling

Run **Distribute stable release** manually from `main`, specifying a version and
one channel, or `all`. Each summary links to the downstream run.
Only the current latest stable release can be promoted by this coordinator.
Drafts, prereleases, invalid versions, missing archives and incomplete manifests
are rejected. Channels recheck latest before promotion.

A successful npm publication followed by a failed source commit can be retried:
the same tarball is checked rather than overwritten. Homebrew commits are
idempotent. Linux keeps published packages and can redeploy its snapshot.
If the coordinator times out, inspect the downstream workflow before retrying;
timeout does not cancel an in-progress signing/deployment job.

The automation does not use a release as permission to upgrade project
dependencies, change SDK contracts, or restart user servers.

## SDK, cluster and binary compatibility

When SDK contracts change, publish the SDK first, update and validate both server
and RedisAdapter against that published version without local replaces, then
publish RedisAdapter before GOX Server. Check RedisAdapter's
`COMPATIBILITY.md`: matching module versions alone do not prove wire
compatibility. RedisAdapter stays optional and is installed with `gox get`;
the server must not install or force-upgrade it.

Run the explicit compatibility check from this repository:

```sh
GOX_REDIS_ADAPTER_DIR=../redisadapter go test ./internal/deps -run TestSDKVersionCompatibility
python3 scripts/smoke-install.py --project
```

Source and release binaries use `./cmd/gox`. The Go-install smoke test verifies
the executable is named `gox` without an output-name override and tests runtime
plugins with the installer's module cache. Official binary smoke tests use a
separate module cache to verify portability. Release builds retain `-trimpath`.

For a manual versioned build, set VERSION to the intended release tag:

```sh
go build -trimpath -ldflags "-X github.com/gox-lang/server/internal/buildinfo.GoxVersion=$VERSION" -o ./build/gox ./cmd/gox
```

Keep Issues, Actions and Private Vulnerability Reporting enabled on official
repositories. Review diffs for credentials, local toolchains and generated output
before committing; ignore rules do not remove files from existing history.

## Action navigation protocol (server v0.1.6)

The Actions client sends `Gox-Action-Redirect: 1` with `Invoke-Action` and uses
`fetch` with `redirect: manual`. A `ctx.Response.Redirect` in that request is
transported as HTTP 200 with `Action-Response: redirect`, the original
`Action-Redirect-Status`, and `Location`. Cookies are preserved, the response has
no body, and `Cache-Control: no-store` prevents caching. This envelope is required
because browser fetch exposes an opaque redirect without readable Location for
manual HTTP redirects. It also prevents 307/308 from replaying Action arguments.

The client navigates the document to the HTTP(S) Location; it does not resolve the
Action promise and run callbacks from the old page while navigation is pending.
Navigation is a fresh browser request, not a repost of Action arguments.
Invalid targets or opaque redirects return an Action error without navigation.
GET/POST requests and older clients without the opt-in header retain ordinary
HTTP redirect responses. No SDK API, session storage, or Redis wire change is
required. Upgrade all HTTP-serving nodes before relying on this feature: a new
client reaching an older node fails closed on an opaque redirect. Refresh pages
to load the new Actions client after the rollout.
