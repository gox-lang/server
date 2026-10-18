package serverruntime

import (
	"strconv"
	"strings"
	"testing"
)

func BenchmarkGoxWriterBuffer(b *testing.B) {
	for _, size := range []int{4 * 1024, 8 * 1024, 16 * 1024} {
		b.Run(strconv.Itoa(size/1024)+"KB", func(b *testing.B) {
			payload := strings.Repeat("x", size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				writer, _ := newTestGoxWriter(b, noCompressionConfig(), "", false)
				writer.WriteRawString(payload)
				if err := writer.Close(); err != nil {
					b.Fatal(err)
				}
				unregisterRuntimeResponse(writer.res.response)
			}
		})
	}
}
