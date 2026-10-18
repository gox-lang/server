package lsp

import (
	"sort"
	"testing"

	"golang.org/x/tools/go/packages"
)

// Phase 15.3c-C2-C2 freezes the public standard-library discovery surface
// before production stops asking go/packages for the "std" pattern. The
// filesystem scanner must match the exact package-name inventory that production
// Discover currently receives from packages.Load(NeedName, "std"), after GOX's
// existing visibility filters are applied. Special packages such as unsafe are
// intentionally part of that surface even though they do not have normal
// CompiledGoFiles.
func TestPhase15_3CC2C2StandardLibraryScannerMatchesGoPackages(t *testing.T) {
	root := phase15C1RepositoryRoot(t)
	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: root}, "std")
	if err != nil {
		t.Fatal(err)
	}

	want := make([]GoPackageCandidate, 0, len(loaded))
	seen := make(map[string]bool, len(loaded))
	for _, candidate := range loaded {
		if candidate == nil || candidate.PkgPath == "" || candidate.Name == "" || candidate.Name == "main" ||
			len(candidate.Errors) > 0 || !goPackageCandidateVisible(candidate.PkgPath) || seen[candidate.PkgPath] {
			continue
		}
		seen[candidate.PkgPath] = true
		want = append(want, GoPackageCandidate{Path: candidate.PkgPath, Name: candidate.Name})
	}
	sort.Slice(want, func(left, right int) bool {
		if want[left].Name != want[right].Name {
			return want[left].Name < want[right].Name
		}
		return want[left].Path < want[right].Path
	})

	got := discoverStandardLibraryGoPackages()
	if diff := phase15C2C2CandidateDiff(want, got); diff != "" {
		t.Fatalf("standard-library scanner differs from go/packages for active toolchain:\n%s", diff)
	}
	t.Logf("standard-library equivalence: %d public importable packages", len(got))
}

func phase15C2C2CandidateDiff(want, got []GoPackageCandidate) string {
	wantByPath := make(map[string]string, len(want))
	gotByPath := make(map[string]string, len(got))
	for _, candidate := range want {
		wantByPath[candidate.Path] = candidate.Name
	}
	for _, candidate := range got {
		gotByPath[candidate.Path] = candidate.Name
	}

	var missing, extra, wrongName []string
	for path, wantName := range wantByPath {
		gotName, ok := gotByPath[path]
		if !ok {
			missing = append(missing, path)
			continue
		}
		if gotName != wantName {
			wrongName = append(wrongName, path+": scanner="+gotName+" go/packages="+wantName)
		}
	}
	for path := range gotByPath {
		if _, ok := wantByPath[path]; !ok {
			extra = append(extra, path)
		}
	}
	if len(missing) == 0 && len(extra) == 0 && len(wrongName) == 0 {
		return ""
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(wrongName)
	return "missing=" + formatStringSlice(missing) + "\nextra=" + formatStringSlice(extra) + "\nwrong-names=" + formatStringSlice(wrongName)
}

func formatStringSlice(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	result := "["
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += value
	}
	return result + "]"
}
