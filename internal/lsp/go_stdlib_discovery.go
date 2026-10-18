package lsp

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// discoverStandardLibraryGoPackages builds the public standard-library package
// inventory directly from GOROOT/src. Regression tests keep this inventory
// equivalent to packages.Load(NeedName, "std") for the active GOX toolchain, so
// Discover can avoid invoking go list for standard packages.
func discoverStandardLibraryGoPackages() []GoPackageCandidate {
	return discoverStandardLibraryGoPackagesWithContext(build.Default)
}

func discoverStandardLibraryGoPackagesWithContext(context build.Context) []GoPackageCandidate {
	if strings.TrimSpace(context.GOROOT) == "" {
		return nil
	}
	sourceRoot := filepath.Join(context.GOROOT, "src")
	if info, err := os.Stat(sourceRoot); err != nil || !info.IsDir() {
		return nil
	}

	candidates := make([]GoPackageCandidate, 0, 256)
	_ = filepath.WalkDir(sourceRoot, func(directory string, entry os.DirEntry, walkErr error) error {
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

		relative, relErr := filepath.Rel(sourceRoot, directory)
		if relErr != nil || relative == "." {
			return nil
		}
		importPath := filepath.ToSlash(relative)
		if importPath == "cmd" || strings.HasPrefix(importPath, "cmd/") || importPath == "builtin" {
			return filepath.SkipDir
		}
		if !goPackageCandidateVisible(importPath) {
			return filepath.SkipDir
		}

		entries, readErr := os.ReadDir(directory)
		if readErr != nil {
			return nil
		}
		packageName, ok := standardLibraryGoPackageName(context, directory, entries)
		if ok && packageName != "main" && packageName != "_" {
			candidates = append(candidates, GoPackageCandidate{Path: importPath, Name: packageName})
		}
		if pruneChildren {
			return filepath.SkipDir
		}
		return nil
	})

	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Name != candidates[right].Name {
			return candidates[left].Name < candidates[right].Name
		}
		return candidates[left].Path < candidates[right].Path
	})
	return candidates
}

// standardLibraryGoPackageName returns the package declared by the first
// non-test Go file selected by the active build context. Unlike workspace
// discovery, standard-library discovery does not need to revalidate every
// active file in the directory: GOROOT is an installed, internally consistent
// toolchain, and an exhaustive regression test verifies the resulting package
// inventory against packages.Load("std"). Stopping after the first active file
// avoids MatchFile and package-clause parsing work for the rest of the package.
func standardLibraryGoPackageName(context build.Context, directory string, entries []os.DirEntry) (string, bool) {
	for _, file := range entries {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		matches, matchErr := context.MatchFile(directory, file.Name())
		if matchErr != nil || !matches {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, file.Name()), nil, parser.PackageClauseOnly)
		if parseErr != nil || parsed.Name == nil || parsed.Name.Name == "" {
			return "", false
		}
		return parsed.Name.Name, true
	}
	return "", false
}
