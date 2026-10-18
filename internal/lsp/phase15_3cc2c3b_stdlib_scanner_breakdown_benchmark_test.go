package lsp

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type phase15C3BStdlibFile struct {
	directory string
	name      string
	path      string
}

type phase15C3BStdlibDirectory struct {
	path       string
	importPath string
}

// Phase 15.3c-C2-C3b isolates the major pieces of the GOROOT scanner. Setup
// that discovers the inputs for an isolated stage is intentionally outside the
// benchmark timer; the production-shaped end-to-end cost remains covered by
// BenchmarkPhase15_3CC2C3StandardLibraryScanner.
func BenchmarkPhase15_3CC2C3BStandardLibraryWalkDirOnly(b *testing.B) {
	context := build.Default
	sourceRoot := filepath.Join(context.GOROOT, "src")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		directories := 0
		err := phase15C3BWalkStdlibDirectories(sourceRoot, func(phase15C3BStdlibDirectory) {
			directories++
		})
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(directories), "dirs/op")
	}
}

func BenchmarkPhase15_3CC2C3BStandardLibraryReadDirGoFilesOnly(b *testing.B) {
	context := build.Default
	directories := phase15C3BStdlibDirectories(b, context)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		goFiles := 0
		for _, directory := range directories {
			entries, err := os.ReadDir(directory.path)
			if err != nil {
				b.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				goFiles++
			}
		}
		b.ReportMetric(float64(goFiles), "go-files/op")
	}
}

func BenchmarkPhase15_3CC2C3BStandardLibraryMatchFileOnly(b *testing.B) {
	context := build.Default
	files := phase15C3BStdlibGoFiles(b, context)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		active := 0
		for _, file := range files {
			matches, err := context.MatchFile(file.directory, file.name)
			if err != nil {
				continue
			}
			if matches {
				active++
			}
		}
		b.ReportMetric(float64(active), "active-files/op")
	}
}

func BenchmarkPhase15_3CC2C3BStandardLibraryPackageClauseOnly(b *testing.B) {
	context := build.Default
	files := phase15C3BActiveStdlibGoFiles(b, context)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parsedFiles := 0
		for _, file := range files {
			parsed, err := parser.ParseFile(token.NewFileSet(), file.path, nil, parser.PackageClauseOnly)
			if err != nil || parsed.Name == nil || parsed.Name.Name == "" {
				continue
			}
			parsedFiles++
		}
		b.ReportMetric(float64(parsedFiles), "parsed-files/op")
	}
}

func BenchmarkPhase15_3CC2C3BStandardLibraryCandidateAssemblyOnly(b *testing.B) {
	base := discoverStandardLibraryGoPackages()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates := make([]GoPackageCandidate, 0, len(base))
		for _, candidate := range base {
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(left, right int) bool {
			if candidates[left].Name != candidates[right].Name {
				return candidates[left].Name < candidates[right].Name
			}
			return candidates[left].Path < candidates[right].Path
		})
		b.ReportMetric(float64(len(candidates)), "candidates/op")
	}
}

func phase15C3BStdlibDirectories(tb testing.TB, context build.Context) []phase15C3BStdlibDirectory {
	tb.Helper()
	sourceRoot := filepath.Join(context.GOROOT, "src")
	var directories []phase15C3BStdlibDirectory
	if err := phase15C3BWalkStdlibDirectories(sourceRoot, func(directory phase15C3BStdlibDirectory) {
		directories = append(directories, directory)
	}); err != nil {
		tb.Fatal(err)
	}
	return directories
}

func phase15C3BStdlibGoFiles(tb testing.TB, context build.Context) []phase15C3BStdlibFile {
	tb.Helper()
	directories := phase15C3BStdlibDirectories(tb, context)
	var files []phase15C3BStdlibFile
	for _, directory := range directories {
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			files = append(files, phase15C3BStdlibFile{
				directory: directory.path,
				name:      entry.Name(),
				path:      filepath.Join(directory.path, entry.Name()),
			})
		}
	}
	return files
}

func phase15C3BActiveStdlibGoFiles(tb testing.TB, context build.Context) []phase15C3BStdlibFile {
	tb.Helper()
	files := phase15C3BStdlibGoFiles(tb, context)
	active := make([]phase15C3BStdlibFile, 0, len(files))
	for _, file := range files {
		matches, err := context.MatchFile(file.directory, file.name)
		if err == nil && matches {
			active = append(active, file)
		}
	}
	return active
}

func phase15C3BWalkStdlibDirectories(sourceRoot string, visit func(phase15C3BStdlibDirectory)) error {
	return filepath.WalkDir(sourceRoot, func(directory string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.IsDir() {
			return nil
		}
		if directory == sourceRoot {
			return nil
		}

		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" {
			return filepath.SkipDir
		}
		pruneChildren := name == "vendor" || name == "internal"

		relative, err := filepath.Rel(sourceRoot, directory)
		if err != nil || relative == "." {
			return nil
		}
		importPath := filepath.ToSlash(relative)
		if importPath == "cmd" || strings.HasPrefix(importPath, "cmd/") || importPath == "builtin" {
			return filepath.SkipDir
		}
		if !goPackageCandidateVisible(importPath) {
			return filepath.SkipDir
		}

		visit(phase15C3BStdlibDirectory{path: directory, importPath: importPath})
		if pruneChildren {
			return filepath.SkipDir
		}
		return nil
	})
}
