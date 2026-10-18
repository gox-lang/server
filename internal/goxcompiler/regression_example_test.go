package gox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegressionExampleGeneratedGo(t *testing.T) {
	source, err := os.ReadFile("testdata/regression_page.gox")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := ParseModule("pages/index.gox", string(source))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	AddActionsClientScriptDecl(bundle, "", nil)
	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	path := filepath.Join(root, "generated.go")
	if err := WriteBundle(bundle, path); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	phase1GoTestGeneratedPackage(t, root, string(generated))
}
