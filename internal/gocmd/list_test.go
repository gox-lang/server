package gocmd

import "testing"

func TestParseModuleList(t *testing.T) {
	modules, err := parseModuleList("example.com/b\tv1.2.0\n\nexample.com/a\tv1.0.0\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 {
		t.Fatalf("len(modules) = %d, want 2", len(modules))
	}
	if modules[0].Path != "example.com/b" || modules[0].Version != "v1.2.0" {
		t.Fatalf("modules[0] = %#v", modules[0])
	}
	if modules[1].Path != "example.com/a" || modules[1].Version != "v1.0.0" {
		t.Fatalf("modules[1] = %#v", modules[1])
	}
}

func TestParseModuleListRejectsInvalidEntry(t *testing.T) {
	if _, err := parseModuleList("example.com/pkg v1.0.0\n"); err == nil {
		t.Fatal("parseModuleList() error = nil, want error")
	}
}
