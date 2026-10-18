package serverruntime

import (
	"strings"
	"testing"
)

func benchmarkWriterWrite(b *testing.B, write func(*GoxWriter)) {
	b.Helper()
	writer, _ := newTestGoxWriter(b, noCompressionConfig(), "", false)
	registeredResponse := writer.res.response

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer.pos = 0
		writer.res.bodyConstructed = false
		writer.res.bodyWritten = false
		write(writer)
	}
	b.StopTimer()

	_ = writer.Close()
	unregisterRuntimeResponse(registeredResponse)
}

func BenchmarkGoxWriterRawString(b *testing.B) {
	benchmarkWriterWrite(b, func(writer *GoxWriter) {
		writer.WriteRawString("<h1>Hello</h1>")
	})
}

func BenchmarkGoxWriterEscapedString(b *testing.B) {
	input := strings.Repeat(`<span title="a&b">x</span>`, 16)
	benchmarkWriterWrite(b, func(writer *GoxWriter) {
		writer.WriteTextString(input)
	})
}
