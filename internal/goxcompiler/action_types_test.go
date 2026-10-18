package gox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUpdateActionTypesWritesOverloadsAndReturnUnion(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "index.gox")
	second := filepath.Join(root, "admin.gox")
	if err := os.WriteFile(first, []byte("module main\nimport \"github.com/gox-lang/gox\"\nfunc Login(email string) gox.Action { if email == \"\" { return false }; return \"ok\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("module main\nimport \"github.com/gox-lang/gox\"\nfunc Login(email string, password string) gox.Action { return true }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second} {
		graph, err := LoadGraph(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := updateActionTypes(graph); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if _, err := os.Stat(filepath.Join(root, ".build", "cache", "actions-types.json")); err != nil {
		t.Fatalf("missing action type cache: %v", err)
	}
	for _, want := range []string{"Defined in `index.gox`", "Defined in `admin.gox`", "Login(email: string)", "Login(email: string, password: string)", "boolean | string", "GoxActionError"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestUpdateActionTypesDropsDeletedSources(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old.gox")
	current := filepath.Join(root, "index.gox")
	if err := os.WriteFile(old, []byte("module main\nimport \"github.com/gox-lang/gox\"\nfunc Old() gox.Action { return true }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	graph, err := LoadGraph(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateActionTypes(graph); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("module main\nimport \"github.com/gox-lang/gox\"\nfunc Current() gox.Action { return true }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	graph, err = LoadGraph(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateActionTypes(graph); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Old(") {
		t.Fatalf("deleted source remained in declarations:\n%s", data)
	}
}

func TestUpdateActionTypesGeneratesJSONShapes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "index.gox")
	source := `module main
import "github.com/gox-lang/gox"
type User struct {
    ID int ` + "`json:\"id\"`" + `
    Name string ` + "`json:\"name\"`" + `
    Secret string ` + "`json:\"-\"`" + `
}
type Request struct {
    User User ` + "`json:\"user\"`" + `
    Tags []string ` + "`json:\"tags\"`" + `
    Scores map[string]float64 ` + "`json:\"scores\"`" + `
}
func Save(input Request) gox.Action { return input.User }
`
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	graph, err := LoadGraph(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateActionTypes(graph); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`input: { user: { id: number; name: string }; tags: Array<string>; scores: Record<string, number> }`,
		`{ id: number; name: string } | null, GoxActionError | null`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Secret") || strings.Contains(text, "secret") {
		t.Fatalf("json ignored field leaked into declaration:\n%s", text)
	}
}

func TestUpdateActionTypesGeneratesAliasesSlicesAndMaps(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "index.gox")
	source := `module main
import "github.com/gox-lang/gox"
type IDs []int
type Lookup map[string]bool
func Find(ids IDs, flags Lookup) gox.Action { return ids }
`
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	graph, err := LoadGraph(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateActionTypes(graph); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`ids: Array<number>`,
		`flags: Record<string, boolean>`,
		`Array<number> | null, GoxActionError | null`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestUpdateActionTypesConcurrentProjectAggregation(t *testing.T) {
	root := t.TempDir()
	const count = 12
	graphs := make([]*ModuleGraph, 0, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("page-%02d.gox", i))
		source := fmt.Sprintf("module main\nimport \"github.com/gox-lang/gox\"\nfunc Action%02d(value string) gox.Action { return value }\n", i)
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
		graph, err := LoadGraph(path)
		if err != nil {
			t.Fatal(err)
		}
		graphs = append(graphs, graph)
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(graphs))
	for _, graph := range graphs {
		wg.Add(1)
		go func(graph *ModuleGraph) {
			defer wg.Done()
			errs <- updateActionTypes(graph)
		}(graph)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for i := 0; i < count; i++ {
		want := fmt.Sprintf("Action%02d(value: string)", i)
		if !strings.Contains(text, want) {
			t.Fatalf("concurrent aggregation lost %q:\n%s", want, text)
		}
	}
}

func TestUpdateActionTypesForModuleUsesUnsavedSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	disk := "module index\nimport \"github.com/gox-lang/gox\"\nfunc Login(email string) gox.Action { return true }\n"
	if err := os.WriteFile(path, []byte(disk), 0644); err != nil {
		t.Fatal(err)
	}
	unsaved := "module index\nimport \"github.com/gox-lang/gox\"\nfunc Login(email string, password string) gox.Action { return \"ok\" }\n"
	mod, err := ParseProjectModule(root, path, unsaved)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateActionTypesForModule(root, mod); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Login(email: string, password: string)") || !strings.Contains(text, "string | null, GoxActionError | null") {
		t.Fatalf("unsaved Action source was not authoritative:\n%s", text)
	}
	if strings.Contains(text, "Login(email: string):") {
		t.Fatalf("disk Action leaked over unsaved source:\n%s", text)
	}
}

func TestRemoveActionTypesForSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	mod, err := ParseProjectModule(root, path, "module index\nimport \"github.com/gox-lang/gox\"\nfunc Login() gox.Action { return true }\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateActionTypesForModule(root, mod); err != nil {
		t.Fatal(err)
	}
	if err := RemoveActionTypesForSource(root, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Login(") {
		t.Fatalf("removed source remained in Action typings:\n%s", data)
	}
}

func TestPruneActionTypesSourcesUsesProjectIndex(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "pages", "index.gox")
	stale := filepath.Join(root, "pages", "old.gox")
	for path, name := range map[string]string{keep: "Current", stale: "Old"} {
		mod, err := ParseProjectModule(root, path, fmt.Sprintf("module index\nimport \"github.com/gox-lang/gox\"\nfunc %s() gox.Action { return true }\n", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := UpdateActionTypesForModule(root, mod); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneActionTypesSources(root, []string{keep}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Current(") || strings.Contains(text, "Old(") {
		t.Fatalf("project-index prune produced wrong Action typings:\n%s", text)
	}
}

func TestUpdateActionTypesForModuleConcurrentAggregation(t *testing.T) {
	root := t.TempDir()
	const count = 12
	mods := make([]*Module, 0, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root, "pages", fmt.Sprintf("page-%02d.gox", i))
		source := fmt.Sprintf("module page%02d\nimport \"github.com/gox-lang/gox\"\nfunc Action%02d(value string) gox.Action { return value }\n", i, i)
		mod, err := ParseProjectModule(root, path, source)
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, mod)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(mods))
	for _, mod := range mods {
		wg.Add(1)
		go func(mod *Module) {
			defer wg.Done()
			errs <- UpdateActionTypesForModule(root, mod)
		}(mod)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for i := 0; i < count; i++ {
		if want := fmt.Sprintf("Action%02d(value: string)", i); !strings.Contains(text, want) {
			t.Fatalf("incremental aggregation lost %q:\n%s", want, text)
		}
	}
}
