# GOX Server

[Official website](https://gox-lang.dev)

The compiler, development server, and command-line tools for GOX. Build web
applications with Go handlers, HTML templates, Actions, WebSockets, and client
components using React, Preact, Solid, Vue, or Svelte.

- [GOX SDK](https://github.com/gox-lang/gox) — application APIs and server contracts.
- [Demo](https://github.com/gox-lang/demo) — interactive examples and a demo store.
- [RedisAdapter](https://github.com/gox-lang/redisadapter) — optional shared sessions
  and communication between server instances.

## Build the CLI

GOX is available for Linux and macOS. Windows is not supported.

Install Go matching the version required by [go.mod](go.mod), or a newer
compatible version. Run the commands below from a checkout of this repository.

### Linux and macOS

Build and install the executable in `/usr/local/bin`, so it is available from
any project directory:

```sh
go build -o ./build/gox .
sudo install -m 755 ./build/gox /usr/local/bin/gox
```

If `/usr/local/bin` is not already in your `PATH`, add the following line to
your shell configuration (`~/.bashrc` for Bash or `~/.zshrc` for Zsh), then open
a new terminal:

```sh
export PATH="/usr/local/bin:$PATH"
```

### Verify the installation

From any directory:

```sh
gox version
gox --help
```

GOX provisions its managed Go, Node.js, and pnpm toolchains when needed;
initial setup requires internet access. To update a locally built installation,
rebuild from the updated checkout and repeat the install command above.

## Start a project

```sh
gox createproject my-app
cd my-app
gox run
```

To start with the complete demo:

```sh
gox createproject my-demo --demo
cd my-demo
gox run
```

`gox createproject --demo` uses `demo` as the project name. The destination must
not already exist. GOX creates the normal scaffold, sets up React, Preact, Solid,
Vue, and Svelte, then downloads the demo sources from `gox-lang/demo`.

## Client components and dependencies

From an existing project:

```sh
gox client add react preact solid vue svelte
gox get github.com/gox-lang/redisadapter
```

RedisAdapter is optional and must be configured explicitly. GOX does not
install it automatically. See its repository for Redis requirements and setup.

Use `gox --help` and `gox <command> --help` for available commands and options.

## Development

```sh
go test ./...
go vet ./...
node --test internal/websocket/*.test.js internal/clientbuilder/*.test.mjs
```

Some integration tests require additional tools or services and skip when their
prerequisites are unavailable. For SDK and cluster changes, also run the SDK's
tests and RedisAdapter's integration suite with an isolated Redis instance.
See [AGENTS.md](AGENTS.md) for the compatibility requirements.

Before a coordinated release, verify the adapter checkout uses the same
published SDK version as this server:

```sh
GOX_REDIS_ADAPTER_DIR=../redisadapter go test ./internal/deps -run TestSDKVersionCompatibility
```

Both consumers currently require GOX SDK `v1.1.0`. Local SDK replacement
directives must not be committed.

See [RELEASING.md](RELEASING.md) for repository setup and release order.

## Contributing and security

We welcome bug reports and proposals. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the contribution policy and [SECURITY.md](SECURITY.md) for private
vulnerability reporting.

## License

[MIT](LICENSE), copyright 2026 Javier Marquez.
