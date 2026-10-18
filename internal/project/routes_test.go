package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesRejectCaseInsensitiveDuplicates(t *testing.T) {
	root := t.TempDir()
	pagesDir := filepath.Join(root, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user.gox", "User.gox"} {
		if err := os.WriteFile(filepath.Join(pagesDir, name), []byte("module user\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := (&Project{RootDir: root}).Routes()
	if err == nil {
		t.Fatal("expected routes that differ only by case to be rejected")
	}
	if !strings.Contains(err.Error(), "duplicate route /user") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRoutesExcludePrivateAndAliasModules(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"pages/index.gox",
		"pages/dashboard/dashboard.gox",
		"pages/user.private.gox",
		"pages/@layout.gox",
		"pages/@components/menu.gox",
		"pages/dashboard/@components/header.gox",
	}
	for _, name := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	routes, err := (&Project{RootDir: root}).Routes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Route != "/" || routes[1].Route != "/dashboard" {
		t.Fatalf("routes = %#v", routes)
	}
}

func TestRoutesPreferDirectFileOverHomonymousFolderFile(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"pages/dashboard.gox", "pages/dashboard/dashboard.gox"} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module dashboard\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	routes, err := (&Project{RootDir: root}).Routes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Route != "/dashboard" || routes[0].File != "pages/dashboard.gox" {
		t.Fatalf("routes = %#v", routes)
	}
}

func TestCleanRouteRemovesOnlyFinalHomonymousSegment(t *testing.T) {
	tests := map[string]string{
		"pages/dashboard.gox":                               "/dashboard",
		"pages/dashboard/dashboard.gox":                     "/dashboard",
		"pages/dashboard/dashboard/dashboard.gox":           "/dashboard/dashboard",
		"pages/dashboard/dashboard/dashboard/dashboard.gox": "/dashboard/dashboard/dashboard",
		"pages/dashboard/index.gox":                         "/dashboard/index",
	}
	for file, want := range tests {
		if got := CleanRoute(file); got != want {
			t.Errorf("CleanRoute(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestResolvePageRouteUsesCanonicalPriority(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"pages/index.gox",
		"pages/dashboard.gox",
		"pages/dashboard/dashboard.gox",
		"pages/dashboard/dashboard/dashboard.gox",
		"pages/dashboard/index.gox",
	} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module page\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := &Project{RootDir: root}
	tests := map[string]string{
		"/":                    "pages/index.gox",
		"/index":               "pages/index.gox",
		"/DASHBOARD/":          "pages/dashboard.gox",
		"/dashboard/dashboard": "pages/dashboard/dashboard/dashboard.gox",
		"/dashboard/index":     "pages/dashboard/index.gox",
	}
	for route, want := range tests {
		entry, ok, err := p.ResolvePageRoute(route)
		if err != nil {
			t.Fatalf("ResolvePageRoute(%q): %v", route, err)
		}
		if !ok || entry.File != want {
			t.Errorf("ResolvePageRoute(%q) = (%#v, %v), want %q", route, entry, ok, want)
		}
	}

	if _, ok, err := p.ResolvePageRoute("/missing"); err != nil || ok {
		t.Fatalf("ResolvePageRoute(/missing) = ok %v, err %v", ok, err)
	}
}

func TestResolvePageRouteUsesHomonymousFallback(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "pages", "dashboard", "dashboard.gox")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("module dashboard\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, ok, err := (&Project{RootDir: root}).ResolvePageRoute("/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || entry.File != "pages/dashboard/dashboard.gox" {
		t.Fatalf("entry = %#v, ok = %v", entry, ok)
	}
	if _, ok, err := (&Project{RootDir: root}).ResolvePageRoute("/dashboard/dashboard"); err != nil || ok {
		t.Fatalf("nested route = ok %v, err %v", ok, err)
	}
}
