package serverruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeGoxRoutePathIsCaseInsensitive(t *testing.T) {
	if got, want := normalizeGoxRoutePath("/User/Profile"), "/user/profile"; got != want {
		t.Fatalf("normalizeGoxRoutePath() = %q, want %q", got, want)
	}
}

func TestResolveGoxRouteFileIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	pageDir := filepath.Join(root, "pages", "Users")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(pageDir, "Profile.gox")
	if err := os.WriteFile(pagePath, []byte("module profile\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := &Runtime{ProjectRoot: root}
	for _, routePath := range []string{"/users/profile", "/Users/Profile", "/USERS/PROFILE"} {
		file, ok, err := runtime.resolveGoxRouteFile(normalizeGoxRoutePath(routePath), nil)
		if err != nil {
			t.Fatalf("%s: %v", routePath, err)
		}
		if !ok {
			t.Fatalf("%s did not resolve", routePath)
		}
		if file.absPath != pagePath {
			t.Fatalf("%s resolved %q, want %q", routePath, file.absPath, pagePath)
		}
	}
}

func TestResolveGoxRouteFileRejectsCaseInsensitiveCollision(t *testing.T) {
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

	runtime := &Runtime{ProjectRoot: root}
	_, _, err := runtime.resolveGoxRouteFile("/user", nil)
	if err == nil {
		t.Fatal("expected routes that differ only by case to be rejected")
	}
}

func TestResolveGoxRouteFileResolvesRootIndex(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte("module index\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := &Runtime{ProjectRoot: root}
	file, ok, err := runtime.resolveGoxRouteFile(normalizeGoxRoutePath("/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("/ did not resolve pages/index.gox")
	}
	if file.absPath != pagePath {
		t.Fatalf("/ resolved %q, want %q", file.absPath, pagePath)
	}
}

func TestResolveGoxRouteFileUsesCanonicalHomonymousRoute(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"pages/dashboard.gox",
		"pages/dashboard/dashboard.gox",
		"pages/dashboard/dashboard/dashboard.gox",
	}
	for _, rel := range files {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runtime := &Runtime{ProjectRoot: root}
	tests := []struct {
		route string
		want  string
	}{
		{route: "/dashboard", want: "pages/dashboard.gox"},
		{route: "/dashboard/dashboard", want: "pages/dashboard/dashboard/dashboard.gox"},
	}
	for _, test := range tests {
		file, ok, err := runtime.resolveGoxRouteFile(test.route, nil)
		if err != nil {
			t.Fatalf("%s: %v", test.route, err)
		}
		if !ok {
			t.Fatalf("%s did not resolve", test.route)
		}
		if got := filepath.ToSlash(file.relPath); got != test.want {
			t.Fatalf("%s resolved %q, want %q", test.route, got, test.want)
		}
	}
}

func TestResolveGoxRouteFileDoesNotDuplicateHomonymousSegment(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "pages", "dashboard", "dashboard.gox")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("module dashboard\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := &Runtime{ProjectRoot: root}
	resolved, ok, err := runtime.resolveGoxRouteFile("/dashboard", nil)
	if err != nil || !ok {
		t.Fatalf("/dashboard resolution = (%#v, %v, %v)", resolved, ok, err)
	}
	if _, ok, err := runtime.resolveGoxRouteFile("/dashboard/dashboard", nil); err != nil || ok {
		t.Fatalf("/dashboard/dashboard resolution = (%v, %v), want not found", ok, err)
	}
}
