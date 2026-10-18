package serverruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/watcher"
)

func TestPageTreeHotReloadRemovesDescendants(t *testing.T) {
	for _, rename := range []bool{false, true} {
		name := "remove"
		if rename {
			name = "rename"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			old := filepath.Join(root, "pages", "products")
			r := &Runtime{goxRoutes: make(map[string]*GoxRouteRuntime), hotReload: hotreload.NewIndex()}
			for _, rel := range []string{"products/products.gox", "products/nested/detail.gox", "products-extra/keep.gox"} {
				file := filepath.Join(root, "pages", rel)
				if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("module example"), 0644); err != nil {
					t.Fatal(err)
				}
				r.goxRoutes[rel] = &GoxRouteRuntime{RoutePath: rel, RelPath: "pages/" + rel, AbsPath: file, State: GoxRouteLoaded}
			}
			op := watcher.OpRemove
			if rename {
				op = watcher.OpRename
				if err := os.Rename(old, filepath.Join(root, "pages", "store")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.RemoveAll(old); err != nil {
				t.Fatal(err)
			}
			// A deleted directory has IsDir=false in fsnotify normalization.
			r.handlePageTreeEvent(watcher.Event{Path: old, Rel: "pages/products", Op: op, Exists: false})
			if len(r.goxRoutes) != 1 || r.goxRoutes["products-extra/keep.gox"] == nil {
				t.Fatalf("remaining routes: %#v", r.goxRoutes)
			}
		})
	}
}

func TestPageTreeHotReloadPreservesRecreatedDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "index.gox")
	if err := os.WriteFile(file, []byte("module index"), 0644); err != nil {
		t.Fatal(err)
	}
	route := &GoxRouteRuntime{RoutePath: "/", AbsPath: file, State: GoxRouteLoaded}
	r := &Runtime{goxRoutes: map[string]*GoxRouteRuntime{"/": route}}
	r.handlePageTreeEvent(watcher.Event{Path: root, Op: watcher.OpRename, Exists: false})
	if r.goxRoutes["/"] != route {
		t.Fatal("recreated source was unmounted")
	}
}
