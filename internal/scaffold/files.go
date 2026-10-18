package scaffold

import (
	"encoding/json"

	"github.com/gox-lang/server/internal/deps"
)

type File struct {
	Path    string
	Content string
}

func goxJSONContent(opts Options) string {
	manifest := opts.Manifest
	if manifest.Dependencies == nil {
		manifest = deps.NewGoxJSON()
		manifest.Dependencies[opts.GoxModule] = opts.GoxModuleVersion
	}
	if manifest.Frameworks == nil {
		manifest.Frameworks = deps.Frameworks{}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(data) + "\n"
}

func ProjectFiles(opts Options) []File {
	return []File{
		{
			Path:    "gox.json",
			Content: goxJSONContent(opts),
		},
		{
			Path: "go.mod",
			Content: `module ` + opts.ProjectName + `

go ` + opts.GoVersion + `

require ` + opts.GoxModule + ` ` + opts.GoxModuleVersion + `

`,
		},
		{
			Path: "app/config.gox",
			Content: `module app

import (
	server "github.com/gox-lang/gox/server"

	http "http.gox"
	ratelimit "ratelimit.gox"
	routes "routes.gox"
	session "session.gox"
	shutdown "shutdown.gox"
	startup "startup.gox"
	static "static.gox"
	websocket "websocket.gox"
)

func SetGoxConfig() server.Config {
	return server.Config{
		ProjectName: "` + opts.ProjectName + `",

		Dev: server.DevConfig{
			Enabled: true,
			Logs: server.DevLogsConfig{
				Static:    false,
				GoxFile:   true,
				Trace:     false,
				TraceDeep: false,
			},
		},

		HTTP:      http.Config,
		RateLimit: ratelimit.Config,
		Session:   session.Config,
		Static:    static.Config,
		Routes:    routes.Config,
		WebSocket: websocket.Config,

		OnStartServer: startup.OnStartServer,
		OnStopServer:  shutdown.OnStopServer,
	}
}
`,
		},

		{
			Path: "app/startup.gox",
			Content: `module startup

func OnStartServer() error {
	return nil
}
`,
		},
		{
			Path: "app/shutdown.gox",
			Content: `module shutdown

func OnStopServer() error {
	return nil
}
`,
		},
		{
			Path: "app/http.gox",
			Content: `module http

import server "github.com/gox-lang/gox/server"

var Config = server.HTTPConfig{
	Host: "localhost",
	Port: 3000,

	TLS: server.TLSConfig{
		Enabled: false,
		Type: "auto",
		Domains: []string{},
		RedirectHTTP: true,
	},

	MethodsAllowed: []string{
		"GET",
		"HEAD",
		"POST",
		"OPTIONS",
	},

	Compression: server.CompressionConfig{
		Enabled: true,

		OrderPriority: []string{
			"zstd",
			"gzip",
			"brotli",
			"deflate",
		},

		Zstd: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   1,
		},

		Gzip: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   3,
		},

		Brotli: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   3,
		},

		Deflate: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   3,
		},
	},

	ResolverIPForwarded: server.ResolverIPForwardedConfig{
		RequireTrustedProxy: false,

		TrustedProxies: []string{
			"127.0.0.1",
			"10.0.0.0/8",
			"172.16.0.0/12",
		},

		Headers: []string{
			"CF-Connecting-IP",
			"X-Real-IP",
			"X-Forwarded-For",
		},
	},
}
`,
		},
		{
			Path: "app/websocket.gox",
			Content: `module websocket

import (
	time "time"

	server "github.com/gox-lang/gox/server"
)

var Config = server.WebSocketConfig{
	Enabled: false,

	HandshakeTimeout: 10 * time.Second,

	MaxMessageSize: 8 << 20, // 8 MiB
	WriteTimeout:   10 * time.Second,

	Heartbeat: server.WebSocketHeartbeatConfig{
		PingInterval: 25 * time.Second,
		PongTimeout:  60 * time.Second,
	},

	Queue: server.WebSocketQueueConfig{
		MaxMessages: 256,
		MaxBytes:    16 << 20, // 16 MiB
	},

	Buffers: server.WebSocketBufferConfig{
		ReadSize:  4096,
		WriteSize: 4096,
	},

	Compression: server.WebSocketCompressionConfig{
		Enabled: false,
		Level:   1,
	},
}
`,
		},
		{
			Path: "app/ratelimit.gox",
			Content: `module ratelimit

import (
	time "time"

	server "github.com/gox-lang/gox/server"
)

var Config = server.RateLimitConfig{
	Enabled: false,

	Rules: []server.RateLimitRule{
		{
			Name:    "pages",
			Enabled: true,
			Methods: []string{"Get", "Post"},
			Match:   "/",
			Limits: []server.RateLimitLimit{
				{
					Name:    "pages-session",
					Enabled: true,
					By:      []server.RateLimitFilter{server.RateLimitFilterSession, server.RateLimitFilterRoute, server.RateLimitFilterMethod},
					Strategy: server.RateLimitFixedWindow{
						Limit:  10,
						Window: 1 * time.Second,
					},
				},
			},
		},
		{
			Name:    "default",
			Enabled: true,
			Methods: []string{server.RateLimitAll},
			Limits: []server.RateLimitLimit{
				{
					Name:    "default-ip",
					Enabled: true,
					By:      []server.RateLimitFilter{server.RateLimitFilterIP},
					Strategy: server.RateLimitFixedWindow{
						Limit:  100,
						Window: time.Minute,
					},
				},
			},
		},
	},
}
`,
		},
		{
			Path: "app/session.gox",
			Content: `module session

import (
	fmt "fmt"
	time "time"

	server "github.com/gox-lang/gox/server"
)

var Config = server.SessionConfig{
	TTL: 3 * time.Hour,

	OnCreate: func(session *server.Session) {
		fmt.Printf("OnCreate %v\n", session.ID())
	},

	OnExpire: func(session *server.ExpireSession) {
		fmt.Printf("OnExpire %v\n", session.ID())
	},
}
`,
		},
		{
			Path: "app/static.gox",
			Content: `module static

import (
	server "github.com/gox-lang/gox/server"
)

var Config = server.StaticConfig{
	Path:              "/public",
	StreamMinFileSize: 524288,

	Compression: server.CompressionConfig{
		Enabled: true,

		OrderPriority: []string{
			"brotli",
			"gzip",
			"zstd",
			"deflate",
		},

		Gzip: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   6,
		},

		Brotli: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   7,
		},

		Deflate: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   6,
		},

		Zstd: server.CompressionAlgorithmConfig{
			Enabled: true,
			Level:   5,
		},
	},

	Headers: []server.StaticHeaderRule{
		{
			Match: "/file.json",
			Headers: server.Headers{
				"Cache-Control": "public, max-age=60",
			},
		},
		{
			Match: "*.js",
			Headers: server.Headers{
				"Cache-Control": "public, max-age=31536000, immutable",
			},
		},
		{
			Match: "*.css",
			Headers: server.Headers{
				"Cache-Control": "public, max-age=31536000, immutable",
			},
		},
		{
			Match: "*",
			Headers: server.Headers{
				"Cache-Control": "public, max-age=86400",
			},
		},
	},

	Range: []server.StaticRangeRule{
		{
			Match:        "*",
			Enabled:      true,
			MaxChunkSize: 1024 * 1024,
			MinFileSize:  4 * 1024 * 1024,
		},
	},
}`,
		},
		{
			Path: "app/routes.gox",
			Content: `module routes

import (
	server "github.com/gox-lang/gox/server"
)

var Config = server.RoutesConfig{
	Routes: map[string]string{},
}
`,
		},
		{
			Path: "pages/index.gox",
			Content: `module index

import (
	gox "github.com/gox-lang/gox"
)

func GET(ctx *gox.Context) gox.Page {

	return <h1>Hello Gox</h1>
}
`,
		},

		{
			Path: "tests/index.gox",
			Content: `module indextest

import test "github.com/gox-lang/gox/test"

func IndexTest() {
	request := test.NewRequest{
		Method: test.GET,
		URL:    "/index",
	}

	expected := test.NewResponseExpected{
		Status: 200,
		Body:   test.ExpectedBody.Contains("Hello Gox"),
	}

	result, match := test.Execute(
		test.Config{},
		[]test.ExecutePipeLine{
			{
				Request:  request,
				Expected: expected,
			},
		},
	)

	if !match {
		test.Fail("Index Test Fail")
	}

	test.Explain(result)
	test.Save(result)
}
`,
		},
		{
			Path: "pages/.error.gox",
			Content: `module error

import (
	gox "github.com/gox-lang/gox"
)

func Default(ctx *gox.Context) gox.Page {

	return <span>{{ ctx.Error.GetStatusCode() }} - {{ ctx.Error.GetStatusMessage() }}</span>
}

func Error404(ctx *gox.Context) gox.Page {

	return <span>{{ ctx.Error.GetStatusCode() }} - {{ ctx.Error.GetStatusMessage() }}</span>
}

func Error500(ctx *gox.Context) gox.Page {

	return <span>{{ ctx.Error.GetStatusCode() }} - {{ ctx.Error.GetStatusMessage() }}</span>
}
`,
		},
	}
}
