package serverruntime

import (
	"testing"

	"github.com/gox-lang/gox"
)

var benchmarkImportMapSecuritySource string

func benchmarkImportMapWrite(b *testing.B, dynamic bool) {
	entries := [][2]string{{"@gox/actions", "/_gox/client/gox-actions.js"}, {"react", "/_gox/client/vendor/react.js"}}
	script := []byte(`<script type="importmap">{"imports":{"@gox/actions":"/_gox/client/gox-actions.js","react":"/_gox/client/vendor/react.js"}}</script>`)
	const hash = "'sha256-benchmark'"

	writer, _ := newTestGoxWriter(b, noCompressionConfig(), "", false)
	registeredResponse := writer.res.response

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		response := gox.NewResponse()
		if dynamic {
			response.ImportMap.Set("three", "/three.js")
		}
		writer.res.response = response
		writer.pos = 0
		writer.res.bodyConstructed = false
		writer.res.bodyWritten = false

		if dynamic {
			response.ImportMap.Runtime().WriteCompiled(writer, entries, nil, "")
		} else {
			response.ImportMap.Runtime().WriteCompiled(writer, entries, script, hash)
		}
	}
	b.StopTimer()

	benchmarkImportMapSecuritySource = writer.res.response.ImportMap.Runtime().SecuritySource()
	_ = writer.Close()
	unregisterRuntimeResponse(registeredResponse)
}

func BenchmarkImportMapStatic(b *testing.B) {
	benchmarkImportMapWrite(b, false)
}

func BenchmarkImportMapDynamic(b *testing.B) {
	benchmarkImportMapWrite(b, true)
}
