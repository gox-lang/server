package gox

import (
	"strings"
	"testing"
)

func TestImportsPreserveOriginalLiteralRanges(t *testing.T) {
	source := `module page
import (
	"users.gox"
	"fmt"
)
func Build() {}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(module.GoxImports) != 1 || module.GoxImports[0].Source.Range.Start != strings.Index(source, `"users.gox"`) {
		t.Fatalf("GOX imports = %#v", module.GoxImports)
	}
	if len(module.GoImports) != 1 || module.GoImports[0].Source.Range.Start != strings.Index(source, `"fmt"`) {
		t.Fatalf("Go imports = %#v", module.GoImports)
	}
}
