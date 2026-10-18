package serverruntime

import "testing"

func TestPageRelToRouteUsesCanonicalPageRoute(t *testing.T) {
	tests := map[string]string{
		"pages/index.gox":                         "/",
		"pages/dashboard.gox":                     "/dashboard",
		"pages/dashboard/dashboard.gox":           "/dashboard",
		"pages/dashboard/dashboard/dashboard.gox": "/dashboard/dashboard",
		"pages/dashboard/index.gox":               "/dashboard/index",
	}
	for file, want := range tests {
		got, ok := pageRelToRoute(file)
		if !ok || got != want {
			t.Errorf("pageRelToRoute(%q) = (%q, %v), want (%q, true)", file, got, ok, want)
		}
	}
}
