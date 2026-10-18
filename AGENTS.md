# SDK and cluster compatibility

GOX Server and the official RedisAdapter must require the same published
`github.com/gox-lang/gox` version. GOX Server's `internal/buildinfo` SDK version
must also match that requirement. Do not commit local SDK `replace` directives
or placeholder SDK versions. RedisAdapter remains an optional dependency that
users install with `gox get`; the server must not auto-install or force-upgrade it.

When changing SDK contracts, session behavior, WebSocket packets, groups,
channels, or distributed builds, inspect the corresponding implementations in
all three repositories. Add regression coverage for affected behavior and run
SDK tests, relevant server tests, and adapter tests with Redis enabled.

Before release, run the server's `TestSDKVersionCompatibility` with
`GOX_REDIS_ADAPTER_DIR` pointing to the adapter checkout. Run adapter tests with
`GOX_REDIS_TEST_ADDR` pointing to an isolated Redis configured with
`notify-keyspace-events Ex`; tests without that variable skip integration cases.
If a contract needs a newer SDK, publish that SDK version first and update both
consumers together. Validate using the published module, without local replaces.
Matching SDK versions alone do not establish wire compatibility: document any
cluster protocol change and whether all nodes must be upgraded together.
