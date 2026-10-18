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

The server source can be built with `go build -o ./build/gox .`. Publish CLI
binaries under the executable name `gox`; the module's final path segment is
`server`, so a plain `go install` of the module does not give the desired CLI name.
For versioned builds, set the version through:

```sh
go build -ldflags "-X github.com/gox-lang/server/internal/buildinfo.GoxVersion=$VERSION" -o ./build/gox .
```

Set `VERSION` to the release tag before running this command.
