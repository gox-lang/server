package gox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type phase16CompileFixture struct {
	root  string
	entry string
	out   string
}

func TestPhase16_1CompilerPipelineFixtures(t *testing.T) {
	fixtures := []phase16CompileFixture{
		phase16MinimalCompileFixture(t),
		phase16GraphCompileFixture(t, 8),
		phase16GraphStdlibCompileFixture(t, 8),
		phase16ProjectGoCompileFixture(t, 16),
		phase16LargeTemplateCompileFixture(t, 64),
	}
	for _, fixture := range fixtures {
		result, err := Compile(fixture.entry, fixture.out)
		if err != nil {
			t.Fatalf("compile benchmark fixture %s: %v", fixture.entry, err)
		}
		if result == nil || !result.Written || result.OutputBytes <= 0 {
			t.Fatalf("invalid compile result for %s: %#v", fixture.entry, result)
		}
	}
}

func BenchmarkPhase16_1CompilerPipeline(b *testing.B) {
	minimal := phase16MinimalCompileFixture(b)
	graph8 := phase16GraphCompileFixture(b, 8)
	graph8Stdlib := phase16GraphStdlibCompileFixture(b, 8)
	projectGo64 := phase16ProjectGoCompileFixture(b, 64)
	largeTemplate := phase16LargeTemplateCompileFixture(b, 256)

	b.Run("Full/Minimal", func(b *testing.B) {
		phase16BenchmarkCompile(b, minimal)
	})
	b.Run("Full/Graph8", func(b *testing.B) {
		phase16BenchmarkCompile(b, graph8)
	})
	b.Run("Full/Graph8Stdlib", func(b *testing.B) {
		phase16BenchmarkCompile(b, graph8Stdlib)
	})
	b.Run("Full/ProjectGo64", func(b *testing.B) {
		phase16BenchmarkCompile(b, projectGo64)
	})
	b.Run("Full/LargeTemplate256", func(b *testing.B) {
		phase16BenchmarkCompile(b, largeTemplate)
	})

	b.Run("Stage/LoadGraph/Minimal", func(b *testing.B) {
		phase16BenchmarkLoadGraph(b, minimal)
	})
	b.Run("Stage/LoadGraph/Graph8", func(b *testing.B) {
		phase16BenchmarkLoadGraph(b, graph8)
	})
	b.Run("Stage/LoadGraph/Graph8Stdlib", func(b *testing.B) {
		phase16BenchmarkLoadGraph(b, graph8Stdlib)
	})
	b.Run("Stage/LoadGraph/LargeTemplate256", func(b *testing.B) {
		phase16BenchmarkLoadGraph(b, largeTemplate)
	})
	b.Run("Stage/ProjectGoSafety/ProjectGo64", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := validateProjectGoGoroutineSafety(projectGo64.root); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Stage/ResolveGoPackages/ProjectGo64", func(b *testing.B) {
		graph, err := LoadGraph(projectGo64.entry)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := resolveCompileGoPackages(graph); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Stage/Bundle/Graph8", func(b *testing.B) {
		phase16BenchmarkBundle(b, graph8)
	})
	b.Run("Stage/FormatMapped/LargeTemplate256", func(b *testing.B) {
		phase16BenchmarkMappedWriterStage(b, largeTemplate, "format")
	})
	b.Run("Stage/RenderMapped/LargeTemplate256", func(b *testing.B) {
		phase16BenchmarkMappedWriterStage(b, largeTemplate, "render")
	})
	b.Run("Stage/WriteBundle/LargeTemplate256", func(b *testing.B) {
		phase16BenchmarkMappedWriterStage(b, largeTemplate, "write")
	})
}

func BenchmarkPhase16_1LoadGraphGoImportScaling(b *testing.B) {
	for _, modules := range []int{1, 4, 8, 16} {
		fixture := phase16GraphStdlibCompileFixture(b, modules)
		b.Run(fmt.Sprintf("Modules%d", modules), func(b *testing.B) {
			phase16BenchmarkLoadGraph(b, fixture)
		})
	}
}

func BenchmarkPhase16_1ProjectGoSafetyScaling(b *testing.B) {
	for _, files := range []int{0, 16, 64, 256} {
		fixture := phase16ProjectGoCompileFixture(b, files)
		b.Run(fmt.Sprintf("GoFiles%d", files+1), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := validateProjectGoGoroutineSafety(fixture.root); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPhase16_1SourceMapScaling(b *testing.B) {
	for _, nodes := range []int{32, 128, 256, 512} {
		fixture := phase16LargeTemplateCompileFixture(b, nodes)
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			phase16BenchmarkMappedWriterStage(b, fixture, "format")
		})
	}
}

func phase16BenchmarkCompile(b *testing.B, fixture phase16CompileFixture) {
	var total, load, bundle, write time.Duration
	var modules, outputBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := Compile(fixture.entry, fixture.out)
		if err != nil {
			b.Fatal(err)
		}
		total += result.Duration
		load += result.LoadGraphDuration
		bundle += result.BundleDuration
		write += result.WriteDuration
		modules += int64(result.ModuleCount)
		outputBytes += result.OutputBytes
	}
	if b.N == 0 {
		return
	}
	other := total - load - bundle - write
	b.ReportMetric(float64(load.Nanoseconds())/float64(b.N), "loadgraph-ns/op")
	b.ReportMetric(float64(bundle.Nanoseconds())/float64(b.N), "bundle-ns/op")
	b.ReportMetric(float64(write.Nanoseconds())/float64(b.N), "write-ns/op")
	b.ReportMetric(float64(other.Nanoseconds())/float64(b.N), "unattributed-ns/op")
	b.ReportMetric(float64(modules)/float64(b.N), "modules/op")
	b.ReportMetric(float64(outputBytes)/float64(b.N), "generated-B/op")
}

func phase16BenchmarkLoadGraph(b *testing.B, fixture phase16CompileFixture) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadGraph(fixture.entry); err != nil {
			b.Fatal(err)
		}
	}
}

func phase16BenchmarkBundle(b *testing.B, fixture phase16CompileFixture) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		graph, err := LoadGraph(fixture.entry)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := BundleModules(graph); err != nil {
			b.Fatal(err)
		}
	}
}

func phase16BenchmarkMappedWriterStage(b *testing.B, fixture phase16CompileFixture, stage string) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		graph, err := LoadGraph(fixture.entry)
		if err != nil {
			b.Fatal(err)
		}
		bundle, err := BundleModules(graph)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		switch stage {
		case "format":
			if _, _, err := FormatBundleMapped(bundle, fixture.out+"#formatted"); err != nil {
				b.Fatal(err)
			}
		case "render":
			if _, _, err := RenderBundleMapped(bundle, fixture.out); err != nil {
				b.Fatal(err)
			}
		case "write":
			if err := WriteBundle(bundle, fixture.out); err != nil {
				b.Fatal(err)
			}
		default:
			b.Fatalf("unknown writer stage %q", stage)
		}
	}
}

func phase16MinimalCompileFixture(tb testing.TB) phase16CompileFixture {
	tb.Helper()
	root := tb.TempDir()
	phase16Write(tb, filepath.Join(root, "go.mod"), "module example.com/phase16\n\ngo 1.24\n")
	entry := filepath.Join(root, "pages", "index.gox")
	phase16Write(tb, entry, `module index

func GET() string {
	return "hello"
}
`)
	return phase16NewCompileFixture(tb, root, entry, "index.go")
}

func phase16GraphCompileFixture(tb testing.TB, modules int) phase16CompileFixture {
	tb.Helper()
	if modules < 1 {
		modules = 1
	}
	root := tb.TempDir()
	phase16Write(tb, filepath.Join(root, "go.mod"), "module example.com/phase16\n\ngo 1.24\n")

	entry := filepath.Join(root, "pages", "index.gox")
	var src strings.Builder
	src.WriteString("module index\n\n")
	if modules > 1 {
		src.WriteString(`import M01 "./m01.gox"` + "\n")
	}
	src.WriteString("\nfunc GET() string { return \"ok\" }\n")
	phase16Write(tb, entry, src.String())

	for i := 1; i < modules; i++ {
		var child strings.Builder
		child.WriteString(fmt.Sprintf("module m%02d\n\n", i))
		if i+1 < modules {
			child.WriteString(fmt.Sprintf("import M%02d \"./m%02d.gox\"\n\n", i+1, i+1))
		}
		child.WriteString(fmt.Sprintf("func Value%02d() string { return \"m%02d\" }\n", i, i))
		phase16Write(tb, filepath.Join(root, "pages", fmt.Sprintf("m%02d.gox", i)), child.String())
	}

	return phase16NewCompileFixture(tb, root, entry, "graph.go")
}

func phase16GraphStdlibCompileFixture(tb testing.TB, modules int) phase16CompileFixture {
	tb.Helper()
	if modules < 1 {
		modules = 1
	}
	root := tb.TempDir()
	phase16Write(tb, filepath.Join(root, "go.mod"), "module example.com/phase16\n\ngo 1.24\n")

	entry := filepath.Join(root, "pages", "index.gox")
	var src strings.Builder
	src.WriteString("module index\n\nimport \"strings\"\n")
	if modules > 1 {
		src.WriteString(`import M01 "./m01.gox"` + "\n")
	}
	src.WriteString("\nfunc GET() string { return strings.TrimSpace(\" ok \") }\n")
	phase16Write(tb, entry, src.String())

	for i := 1; i < modules; i++ {
		var child strings.Builder
		child.WriteString(fmt.Sprintf("module m%02d\n\nimport \"strings\"\n", i))
		if i+1 < modules {
			child.WriteString(fmt.Sprintf("import M%02d \"./m%02d.gox\"\n", i+1, i+1))
		}
		child.WriteString(fmt.Sprintf("\nfunc Value%02d() string { return strings.TrimSpace(\" m%02d \") }\n", i, i))
		phase16Write(tb, filepath.Join(root, "pages", fmt.Sprintf("m%02d.gox", i)), child.String())
	}

	return phase16NewCompileFixture(tb, root, entry, "graph-stdlib.go")
}

func phase16ProjectGoCompileFixture(tb testing.TB, unrelated int) phase16CompileFixture {
	tb.Helper()
	root := tb.TempDir()
	const modulePath = "example.com/phase16"
	phase16Write(tb, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.24\n")
	phase16Write(tb, filepath.Join(root, "model", "model.go"), `package model

func Name() string { return "George" }
`)
	for i := 0; i < unrelated; i++ {
		phase16Write(tb, filepath.Join(root, "unrelated", fmt.Sprintf("p%03d", i), "value.go"), fmt.Sprintf("package p%03d\n\nfunc Value() int { return %d }\n", i, i))
	}
	entry := filepath.Join(root, "pages", "index.gox")
	phase16Write(tb, entry, `module index

import "example.com/phase16/model"

func GET() string { return model.Name() }
`)
	return phase16NewCompileFixture(tb, root, entry, "project-go.go")
}

func phase16LargeTemplateCompileFixture(tb testing.TB, nodes int) phase16CompileFixture {
	tb.Helper()
	if nodes < 1 {
		nodes = 1
	}
	root := tb.TempDir()
	phase16Write(tb, filepath.Join(root, "go.mod"), "module example.com/phase16\n\ngo 1.24\n")
	entry := filepath.Join(root, "pages", "index.gox")
	var src strings.Builder
	src.Grow(nodes*40 + 256)
	src.WriteString("module index\n\nfunc GET() interface{} {\n\tvalue := \"hello\"\n\treturn <main>\n")
	for i := 0; i < nodes; i++ {
		src.WriteString(fmt.Sprintf("\t\t<span data-index=\"%d\">{{value}}</span>\n", i))
	}
	src.WriteString("\t</main>\n}\n")
	phase16Write(tb, entry, src.String())
	return phase16NewCompileFixture(tb, root, entry, "large.go")
}

func phase16NewCompileFixture(tb testing.TB, root, entry, outputName string) phase16CompileFixture {
	tb.Helper()
	out := filepath.Join(root, ".build", "bench", outputName)
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		tb.Fatal(err)
	}
	return phase16CompileFixture{root: root, entry: entry, out: out}
}

func phase16Write(tb testing.TB, path, contents string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		tb.Fatal(err)
	}
}
