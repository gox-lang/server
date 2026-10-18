package serverruntime

import (
	"testing"

	"github.com/gox-lang/gox"
)

func benchmarkWriteClientIsland(b *testing.B, writeOpen func(*gox.Context), writeChildren func(*gox.Context)) {
	b.Helper()
	writer, _ := newTestGoxWriter(b, noCompressionConfig(), "", false)
	registeredResponse := writer.res.response
	policy := testGoxIslePolicy()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer.pos = 0
		writer.res.bodyConstructed = false
		writer.res.bodyWritten = false

		ctx := gox.Context{Writer: writer}
		gox.BindClientIslandRuntime(&ctx, policy)
		gox.WriteClientIsland(&ctx, testClientKey, writeOpen, writeChildren)
	}
	b.StopTimer()

	_ = writer.Close()
	unregisterRuntimeResponse(registeredResponse)
}

func BenchmarkWriteClientIsland(b *testing.B) {
	benchmarkWriteClientIsland(b, nil, nil)
}

func BenchmarkWriteClientIslandIsolated(b *testing.B) {
	writeOpen := func(ctx *gox.Context) {
		ctx.Writer.WriteRawString(` data-isolate="true"`)
		gox.WriteClientIslandDataAssets(ctx)
	}
	benchmarkWriteClientIsland(b, writeOpen, nil)
}

func BenchmarkWriteClientIslandWithProps(b *testing.B) {
	writeOpen := func(ctx *gox.Context) {
		ctx.Writer.WriteRawString(` data-props="{&#34;title&#34;:&#34;Menu&#34;}"`)
	}
	benchmarkWriteClientIsland(b, writeOpen, nil)
}

func BenchmarkWriteClientIslandWithChildren(b *testing.B) {
	writeChildren := func(ctx *gox.Context) {
		ctx.Writer.WriteRawString(`<div>SSR</div>`)
	}
	benchmarkWriteClientIsland(b, nil, writeChildren)
}
