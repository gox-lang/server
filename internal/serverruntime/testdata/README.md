# serverruntime testdata

Fixtures for static files, range requests, compression artifacts, and runtime integration tests.


## Local ACME integration

The [ACME integration tests](../tls_acme_integration_test.go) run an official
Pebble process with local DNS, isolated storage and real challenge validation.
They cover HTTP-01, TLS-ALPN-01, automatic selection, DNS-01 wildcard issuance,
renewal through certificate maintenance, and restart with the CA offline.
DNS propagation prechecks use a local test override; Pebble still verifies the
actual TXT records. No public CA or DNS provider is contacted.

Install Pebble separately from the server dependency graph, then run:

```sh
GOBIN=/tmp/gox-acme-tools go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1
GOX_TEST_PEBBLE=/tmp/gox-acme-tools/pebble go test -race -tags acmeintegration ./internal/serverruntime -run 'TestACMELocalAuthority|TestAutomaticTLS' -count=1
```

Run these commands from the repository root. The integration build tag requires
the executable explicitly and fails if it is missing. Ordinary Go tests also
cover automatic TLS configuration, listener failure cleanup and cancellation
of an in-flight ACME request using a local HTTP server.

## TLS request-path benchmarks

Run without concurrent test/build workloads:

```sh
GOMAXPROCS=1 go test ./internal/serverruntime -run '^$' -bench 'BenchmarkRuntimeTransport|BenchmarkHTTPSRedirect' -benchmem -count=5
```

These benchmarks compare the same small application response over HTTP, an
already-established HTTPS connection, and a trusted proxy, plus HTTP redirection.
They exclude network latency and TLS cryptography. They are not end-to-end
request latency measurements or a historical performance baseline.

## HTTP protocol integration

[Protocol tests](../http_protocol_test.go) use GOX listeners and real connections
for HTTP/1.1, HTTP/2 and HTTP/3, including streaming and recovered errors.
[Functional tests](../http_protocol_functional_test.go) verify POST bodies, gzip,
secure session cookies shared across protocols, and request cancellation.
[HTTP/3 lifecycle tests](../http3_test.go) cover UDP conflicts, reload rejection,
graceful and forced shutdown, and restart on the same ports. Manual certificate
rotation and local ACME tests also verify certificates through HTTP/3.

```sh
go test -race ./internal/serverruntime -count=1
GOMAXPROCS=1 go test ./internal/serverruntime -run '^$' -bench '^BenchmarkHTTP3Advertisement$' -benchmem -count=5
```

The advertisement benchmark isolates the added response-header cost against the
same application handler. It does not compare TCP and QUIC network performance.
Local clients connect directly to test ports; public Alt-Svc advertises 443 as
required by GOX's deployment contract. These tests do not simulate browser
fallback when UDP is blocked or a real reverse proxy/firewall deployment.
