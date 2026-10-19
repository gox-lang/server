# Publishing GOX Server and RedisAdapter

## Repository setup

Use `gox-lang/server` and `gox-lang/redisadapter` for the repositories. Their Go
module paths must match those names. Create empty repositories when publishing
these existing Git histories; do not generate a second README or license on GitHub.

Enable GitHub Issues, Actions, and Private Vulnerability Reporting in each
repository. The security policy relies on private reporting being enabled.
Configure branch protection after the initial CI run succeeds.

## Validate before publishing

1. Review the complete diff and the files included in each commit. Ignore local
   toolchains, credentials, editor settings, generated projects, and build output.
   Ignore rules do not remove previously committed files from Git history.
2. Run the SDK tests, server tests, and adapter integration tests against an
   isolated Redis with `notify-keyspace-events Ex`.
3. Run `GOX_REDIS_ADAPTER_DIR=../redisadapter go test ./internal/deps -run TestSDKVersionCompatibility`
   from this repository. Both consumers and `internal/buildinfo` must select the
   same published SDK release, without local SDK replacements.
4. Check RedisAdapter's `COMPATIBILITY.md` for cluster upgrade requirements.
   Matching SDK versions do not prove wire compatibility.
5. Commit each repository separately, push, and wait for its GitHub CI to pass.

## Release order

If the SDK contract changed, publish the required SDK version first. Update and
validate both consumers against that published version. Publish RedisAdapter
next, then GOX Server. Choose release tags explicitly; do not move existing tags.

RedisAdapter remains an optional dependency installed with `gox get`. The server
must not install it or force-upgrade it automatically.

The server source can be built with `go build -trimpath -o ./build/gox ./cmd/gox`. Publish CLI
binaries from `./cmd/gox`. Users install the same command with
`go install github.com/gox-lang/server/cmd/gox@latest`.
Before release, run `python3 scripts/smoke-install.py` to verify Go produces
the executable name `gox` without an explicit output override. Run it with
`--project` to verify runtime plugins using the installer’s module cache.
Official binary smoke tests keep a separate module cache to verify portability;
release builds retain `-trimpath` for that purpose.
For versioned builds, set the version through:

```sh
go build -trimpath -ldflags "-X github.com/gox-lang/server/internal/buildinfo.GoxVersion=$VERSION" -o ./build/gox ./cmd/gox
```

Set `VERSION` to the release tag before running this command.
