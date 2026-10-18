package clientframework

import (
	"sort"
	"strings"
)

type Status uint8

const (
	Supported Status = iota
	NotSupportedYet
)

type Definition struct {
	Name       string
	Status     Status
	Extensions []string
}

var definitions = map[string]Definition{
	"react":  {Name: "react", Status: Supported, Extensions: []string{".jsx", ".tsx"}},
	"preact": {Name: "preact", Status: Supported, Extensions: []string{".jsx", ".tsx"}},
	"solid":  {Name: "solid", Status: Supported, Extensions: []string{".jsx", ".tsx"}},
	"vue":    {Name: "vue", Status: Supported, Extensions: []string{".vue"}},
	"svelte": {Name: "svelte", Status: Supported, Extensions: []string{".svelte"}},
	"gox":    {Name: "gox", Status: NotSupportedYet, Extensions: []string{".gox"}},
}

func Lookup(name string) (Definition, bool) {
	def, ok := definitions[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Definition{}, false
	}
	def.Extensions = append([]string(nil), def.Extensions...)
	return def, true
}

func SupportsExtension(name string, extension string) bool {
	def, ok := Lookup(name)
	if !ok {
		return false
	}
	for _, allowed := range def.Extensions {
		if extension == allowed {
			return true
		}
	}
	return false
}

func SupportedNames() []string {
	names := make([]string, 0, len(definitions))
	for name, def := range definitions {
		if def.Status == Supported {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
