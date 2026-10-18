package clientframework

import (
	"reflect"
	"testing"
)

func TestRegistryFrameworkStates(t *testing.T) {
	for _, name := range []string{"react", "preact", "solid", "vue", "svelte"} {
		def, ok := Lookup(name)
		if !ok || def.Status != Supported {
			t.Fatalf("Lookup(%q) = %#v, %v; want supported", name, def, ok)
		}
	}

	gox, ok := Lookup("gox")
	if !ok || gox.Status != NotSupportedYet {
		t.Fatalf("Lookup(gox) = %#v, %v; want not supported yet", gox, ok)
	}
	if _, ok := Lookup("wasm"); ok {
		t.Fatal("wasm must not be registered")
	}
}

func TestSupportedNames(t *testing.T) {
	want := []string{"preact", "react", "solid", "svelte", "vue"}
	if got := SupportedNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedNames() = %#v, want %#v", got, want)
	}
}

func TestSupportsExtension(t *testing.T) {
	tests := []struct {
		framework string
		extension string
		want      bool
	}{
		{framework: "react", extension: ".jsx", want: true},
		{framework: "react", extension: ".tsx", want: true},
		{framework: "preact", extension: ".jsx", want: true},
		{framework: "solid", extension: ".tsx", want: true},
		{framework: "vue", extension: ".vue", want: true},
		{framework: "svelte", extension: ".svelte", want: true},
		{framework: "gox", extension: ".gox", want: true},
		{framework: "vue", extension: ".tsx", want: false},
		{framework: "wasm", extension: ".wasm", want: false},
	}
	for _, tt := range tests {
		if got := SupportsExtension(tt.framework, tt.extension); got != tt.want {
			t.Errorf("SupportsExtension(%q, %q) = %v, want %v", tt.framework, tt.extension, got, tt.want)
		}
	}
}
