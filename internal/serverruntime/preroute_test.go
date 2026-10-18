package serverruntime

import (
	"testing"

	"github.com/gox-lang/gox/server"
)

func TestPreRouterMatchExact(t *testing.T) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{
		"/about": "/pages/about",
	}})
	if err != nil {
		t.Fatalf("NewPreRouter failed: %v", err)
	}

	_, target, params, ok := router.Match("about/")
	if !ok {
		t.Fatal("expected route to match")
	}
	if target != "/pages/about" {
		t.Fatalf("target = %q, want %q", target, "/pages/about")
	}
	if len(params) != 0 {
		t.Fatalf("params = %#v, want empty", params)
	}
}

func TestPreRouterMatchParam(t *testing.T) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{
		"/blog/:slug": "/blog/show",
	}})
	if err != nil {
		t.Fatalf("NewPreRouter failed: %v", err)
	}

	_, target, params, ok := router.Match("/blog/hello-world")
	if !ok {
		t.Fatal("expected route to match")
	}
	if target != "/blog/show" {
		t.Fatalf("target = %q, want %q", target, "/blog/show")
	}
	if got := params["slug"]; got != "hello-world" {
		t.Fatalf("slug = %q, want %q", got, "hello-world")
	}
}

func TestPreRouterMatchOptionalParam(t *testing.T) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{
		"/docs/:section?": "/docs/index",
	}})
	if err != nil {
		t.Fatalf("NewPreRouter failed: %v", err)
	}

	for _, path := range []string{"/docs", "/docs/api"} {
		t.Run(path, func(t *testing.T) {
			_, target, _, ok := router.Match(path)
			if !ok {
				t.Fatal("expected route to match")
			}
			if target != "/docs/index" {
				t.Fatalf("target = %q, want %q", target, "/docs/index")
			}
		})
	}
}

func TestPreRouterMatchWildcard(t *testing.T) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{
		"/assets/*": "/static/assets",
	}})
	if err != nil {
		t.Fatalf("NewPreRouter failed: %v", err)
	}

	for _, path := range []string{"/assets", "/assets/app.css", "/assets/css/app.css"} {
		t.Run(path, func(t *testing.T) {
			_, target, _, ok := router.Match(path)
			if !ok {
				t.Fatal("expected route to match")
			}
			if target != "/static/assets" {
				t.Fatalf("target = %q, want %q", target, "/static/assets")
			}
		})
	}
}

func TestPreRouterChoices(t *testing.T) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{
		"/(en|es)/blog/:slug": "/blog/show",
	}})
	if err != nil {
		t.Fatalf("NewPreRouter failed: %v", err)
	}

	for _, path := range []string{"/en/blog/post", "/es/blog/post"} {
		t.Run(path, func(t *testing.T) {
			_, _, params, ok := router.Match(path)
			if !ok {
				t.Fatal("expected route to match")
			}
			if got := params["slug"]; got != "post" {
				t.Fatalf("slug = %q, want %q", got, "post")
			}
		})
	}

	if _, _, _, ok := router.Match("/fr/blog/post"); ok {
		t.Fatal("did not expect /fr/blog/post to match")
	}
}

func TestPreRouterInvalidWildcardPosition(t *testing.T) {
	_, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{
		"/assets/*/edit": "/bad",
	}})
	if err == nil {
		t.Fatal("expected invalid wildcard route to fail")
	}
}
