package serverruntime

import (
	"strconv"
	"testing"

	"github.com/gox-lang/gox/server"
)

func BenchmarkPreRouterExact(b *testing.B) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{"/about": "/pages/about"}})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = router.Match("/about")
	}
}

func BenchmarkPreRouterParam(b *testing.B) {
	router, err := NewPreRouter(server.RoutesConfig{Routes: map[string]string{"/blog/:slug": "/blog/show"}})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = router.Match("/blog/hello-world")
	}
}

func BenchmarkPreRouterManyRules(b *testing.B) {
	routes := make(map[string]string, 1000)
	for i := 0; i < 1000; i++ {
		routes["/route-"+strconv.Itoa(i)+"/:id"] = "/target-" + strconv.Itoa(i)
	}
	router, err := NewPreRouter(server.RoutesConfig{Routes: routes})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = router.Match("/route-999/abc")
	}
}
