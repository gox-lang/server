package clientbuilder

import (
	"strings"
	"testing"
)

func TestPublicBaseNameNormalizesClientFrameworkPrefix(t *testing.T) {
	want := PublicBaseName("menu", "react", "menu/menu.jsx", "dev")
	for _, path := range []string{
		"client/react/menu/menu.jsx",
		"client/React/menu/menu.jsx",
		"menu/menu.jsx",
	} {
		if got := PublicBaseName("menu", "react", path, "dev"); got != want {
			t.Fatalf("PublicBaseName(%q)=%q, want %q", path, got, want)
		}
	}
}

func TestPublicBaseNameIsIndependentFromDeclarationName(t *testing.T) {
	menu := PublicBaseName("menu", "react", "menu/menu.jsx", "dev")
	reactMenu := PublicBaseName("reactmenu", "react", "menu/menu.jsx", "dev")
	if reactMenu != menu {
		t.Fatalf("PublicBaseName should be based on framework/path/mode, not declaration name: %q != %q", reactMenu, menu)
	}
	if !strings.HasPrefix(menu, "menu-") {
		t.Fatalf("PublicBaseName should still use the source file name as readable prefix, got %q", menu)
	}
}
