# GOX Server

[Official website](https://gox-lang.dev)

The compiler, development server, and command-line tools for GOX. Build web
applications with Go handlers, HTML templates, Actions, WebSockets, and client
components using React, Preact, Solid, Vue, or Svelte.

- [GOX SDK](https://github.com/gox-lang/gox) — application APIs and server contracts.
- [Demo](https://github.com/gox-lang/demo) — interactive examples and a demo store.
- [RedisAdapter](https://github.com/gox-lang/redisadapter) — optional shared sessions
  and communication between server instances.

## Install with Go

The `cmd/gox` entry point is available on `main` and will be included in the
next tagged release. Until then, use `@main` instead of `@latest`.

With Go 1.26 or later:

```sh
go install -trimpath github.com/gox-lang/server/cmd/gox@latest
gox version
```

`-trimpath` keeps the executable compatible with runtime plugins built from
a different module-cache location, as in the official releases.

Go installs the `gox` executable in `GOBIN`, or in `$(go env GOPATH)/bin`
when `GOBIN` is unset. Ensure that directory is on your `PATH`. For the
standard Go configuration, add this line to your shell profile:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

Use Linux, macOS, or WSL to run applications that require Go plugins.
Native Windows supports the CLI and language server, but not Go plugins.

## Build the CLI

GOX runs on Linux, macOS, and Windows. On Windows, `gox ls` provides native
editor support; use WSL for application development and execution because
Go plugins are not supported on native Windows.

Install Go matching the version required by [go.mod](go.mod), or a newer
compatible version. Run the commands below from a checkout of this repository.

### Linux and macOS

Build and install the executable in `/usr/local/bin`, so it is available from
any project directory:

```sh
go build -trimpath -o ./build/gox ./cmd/gox
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

## Visual Studio Code

**GOX Tools** is the official VS Code extension for GOX. It provides syntax
highlighting, snippets, visibility hints, GOX Dark and GOX Light themes, and
language features powered by the `gox ls` language server: completion,
diagnostics, and navigation.

Install [GOX Tools from Visual Studio Marketplace](https://marketplace.visualstudio.com/items?itemName=gox-lang.gox-tools),
or run this command with the VS Code CLI:

```sh
code --install-extension gox-lang.gox-tools
```

With GOX Tools 0.1.4 or newer, the extension automatically locates GOX or
installs an official release for the extension host. No separate Go installation
is required: `gox ls` prepares its managed Go toolchain. Existing installations
and a custom `gox.languageServer.path` are respected.

New VS Code terminals receive the GOX executable directory in `PATH`. Managed
installations receive stable GOX updates through the language server; running
applications are not restarted. Use `gox update` for an explicit update.

VS Code 1.91 or newer is required. Windows supports the native language server;
use VS Code connected to WSL for application development and execution.

## Updates

Run `gox update` to check the official stable release immediately, verify its
SHA-256 checksum, and install the new executable. Existing applications keep
running their current version; project dependencies are not changed.

`gox run` checks in the background at most once every 24 hours and recommends
`gox update` when a newer version is available. It never installs an update.
The check is cached between processes, and network failures do not stop GOX.

`gox ls` can install updates for installations managed by GOX Tools when the
extension enables update coordination. It notifies the extension to restart the
language server after validating and installing the executable. External
installations receive an update notice instead. Development builds do not
participate in automatic stable-release updates.

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

Both consumers currently require GOX SDK `v1.1.1`. Local SDK replacement
directives must not be committed.

See [RELEASING.md](RELEASING.md) for repository setup and release order.

## Contributing and security

We welcome bug reports and proposals. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the contribution policy and [SECURITY.md](SECURITY.md) for private
vulnerability reporting.

## License

[MIT](LICENSE), copyright 2026 Javier Marquez.
