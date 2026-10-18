package gox

import (
	"strings"
	"testing"
)

func TestClientAdaptContract(t *testing.T) {
	source := func(markup string) string {
		return `module page
import gox "github.com/gox-lang/gox"
client Panel react { src: "panel.jsx" }
func GET(ctx *gox.Context) gox.Page { return ` + markup + ` }
`
	}
	for _, value := range ClientAdaptValues() {
		for _, ending := range []string{` />`, `><p>Loading</p></@Panel>`} {
			markup := `<@Panel adapt="` + value + `"` + ending
			if _, err := ParseModule("pages/index.gox", source(markup)); err != nil {
				t.Errorf("%s: %v", markup, err)
			}
		}
	}
	for _, attrs := range []string{`adapt="top-center"`, `adapt="vertical-bottom"`, `adapt="Top-Left"`, `adapt={{ direction }}`, `adapt`, `adapt=""`, `adapt="top bottom"`, `adapt="top" adapt="bottom"`, `data-adapt="top"`} {
		if _, err := ParseModule("pages/index.gox", source(`<@Panel `+attrs+` />`)); err == nil {
			t.Errorf("accepted %s", attrs)
		}
	}
	for _, markup := range []string{`<div adapt="top" />`, `<&Panel adapt="top" />`, `<:if condition={{ true }} adapt="top"><p>x</p></:if>`} {
		_, err := ParseModule("pages/index.gox", source(markup))
		if err == nil || !strings.Contains(err.Error(), "adapt") {
			t.Errorf("%s: %v", markup, err)
		}
	}
}
