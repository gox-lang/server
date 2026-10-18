package clientbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
	"github.com/gox-lang/server/internal/projecttoolchain"
)

func TestProjectsKeepFrontendToolchainsAndFrameworkVersionsIndependent(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()

	manifestA := projectdefaults.Manifest()
	manifestA.Toolchain.Node = "18.20.8"
	manifestA.Toolchain.PNPM = "9.15.9"
	manifestA.Toolchain.Vite = "7.1.0"
	manifestA.Toolchain.TypeScript = "5.9.2"
	manifestA.Frameworks["react"]["react"] = "18.3.1"
	manifestA.Frameworks["react"]["react-dom"] = "18.3.1"
	manifestA.Frameworks["react"]["@vitejs/plugin-react"] = "5.0.0"
	manifestA.Frameworks["react"]["@types/react"] = "18.3.0"
	manifestA.Frameworks["react"]["@types/react-dom"] = "18.3.0"

	manifestB := projectdefaults.Manifest()
	manifestB.Toolchain.Node = "22.18.0"
	manifestB.Toolchain.PNPM = "11.9.0"
	manifestB.Toolchain.Vite = "8.1.0"
	manifestB.Toolchain.TypeScript = "6.0.3"
	manifestB.Frameworks["react"]["react"] = "19.2.0"
	manifestB.Frameworks["react"]["react-dom"] = "19.2.0"
	manifestB.Frameworks["react"]["@vitejs/plugin-react"] = "6.0.3"
	manifestB.Frameworks["react"]["@types/react"] = "19.2.0"
	manifestB.Frameworks["react"]["@types/react-dom"] = "19.2.0"

	if err := deps.WriteGoxJSON(projectA, manifestA); err != nil {
		t.Fatal(err)
	}
	if err := deps.WriteGoxJSON(projectB, manifestB); err != nil {
		t.Fatal(err)
	}

	toolchainA, err := projecttoolchain.Resolve(projectA)
	if err != nil {
		t.Fatal(err)
	}
	toolchainB, err := projecttoolchain.Resolve(projectB)
	if err != nil {
		t.Fatal(err)
	}
	if toolchainA.Node.ExactVersion != "node18.20.8" || toolchainA.PNPM.ExactVersion != "pnpm9.15.9" {
		t.Fatalf("project A toolchain = %#v", toolchainA)
	}
	if toolchainB.Node.ExactVersion != "node22.18.0" || toolchainB.PNPM.ExactVersion != "pnpm11.9.0" {
		t.Fatalf("project B toolchain = %#v", toolchainB)
	}

	if _, err := EnsureFrameworkWorkspace(projectA, "react"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureFrameworkWorkspace(projectB, "react"); err != nil {
		t.Fatal(err)
	}

	readPackage := func(root string) packageJSON {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "client", "react", "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		var pkg packageJSON
		if err := json.Unmarshal(data, &pkg); err != nil {
			t.Fatal(err)
		}
		return pkg
	}

	packageA := readPackage(projectA)
	packageB := readPackage(projectB)

	if packageA.Dependencies["react"] != "18.3.1" || packageA.Dependencies["react-dom"] != "18.3.1" {
		t.Fatalf("project A React dependencies = %#v", packageA.Dependencies)
	}
	if packageA.DevDependencies["vite"] != "7.1.0" || packageA.DevDependencies["typescript"] != "5.9.2" || packageA.DevDependencies["@vitejs/plugin-react"] != "5.0.0" {
		t.Fatalf("project A React devDependencies = %#v", packageA.DevDependencies)
	}
	if packageB.Dependencies["react"] != "19.2.0" || packageB.Dependencies["react-dom"] != "19.2.0" {
		t.Fatalf("project B React dependencies = %#v", packageB.Dependencies)
	}
	if packageB.DevDependencies["vite"] != "8.1.0" || packageB.DevDependencies["typescript"] != "6.0.3" || packageB.DevDependencies["@vitejs/plugin-react"] != "6.0.3" {
		t.Fatalf("project B React devDependencies = %#v", packageB.DevDependencies)
	}
}
