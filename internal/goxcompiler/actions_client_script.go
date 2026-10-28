package gox

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

const (
	goxActionsClientScriptVar    = "goxActionsClientScript"
	goxStaticImportMapEntriesVar = "goxStaticImportMapEntries"
	goxStaticImportMapScriptVar  = "goxStaticImportMapScript"
	goxStaticImportMapHashVar    = "goxStaticImportMapHash"
)

func AddActionsClientScriptDecl(bundle *Bundle, actionsRuntimeURL string, extraImportMapEntries map[string]string) {
	if bundle == nil {
		return
	}
	// Compile replaces the defaults with the resolved client assets.
	kept := bundle.Decls[:0]
	for _, decl := range bundle.Decls {
		generated := false
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR && len(gen.Specs) == 1 {
			if value, ok := gen.Specs[0].(*ast.ValueSpec); ok && len(value.Names) == 1 {
				switch value.Names[0].Name {
				case goxActionsClientScriptVar, goxStaticImportMapEntriesVar, goxStaticImportMapScriptVar, goxStaticImportMapHashVar:
					generated = true
				}
			}
		}
		if !generated {
			kept = append(kept, decl)
		}
	}
	bundle.Decls = kept

	actionsScript := buildActionsMetadataScript(bundleActionSignatures(bundle))
	entries, script, hash := buildStaticImportMap(actionsRuntimeURL, extraImportMapEntries)
	decls := []ast.Decl{
		byteSliceVarDecl(goxActionsClientScriptVar, actionsScript),
		importMapEntriesVarDecl(goxStaticImportMapEntriesVar, entries),
		byteSliceVarDecl(goxStaticImportMapScriptVar, script),
		stringVarDecl(goxStaticImportMapHashVar, hash),
	}
	bundle.Decls = insertDeclsAfterImports(bundle.Decls, decls)
}

func stringVarDecl(name string, value string) ast.Decl {
	return &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names:  []*ast.Ident{ast.NewIdent(name)},
		Values: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}},
	}}}
}

func buildStaticImportMap(actionsRuntimeURL string, extra map[string]string) (entries []importMapEntry, script string, cspHash string) {
	entryMap := map[string]string{}
	actionsRuntimeURL = strings.TrimSpace(actionsRuntimeURL)
	if actionsRuntimeURL != "" {
		entryMap["@gox/actions"] = actionsRuntimeURL
	}
	for specifier, url := range extra {
		specifier = strings.TrimSpace(specifier)
		url = strings.TrimSpace(url)
		if specifier != "" && url != "" {
			entryMap[specifier] = url
		}
	}
	if len(entryMap) == 0 {
		return nil, "", ""
	}

	keys := make([]string, 0, len(entryMap))
	for specifier := range entryMap {
		keys = append(keys, specifier)
	}
	sort.Strings(keys)
	entries = make([]importMapEntry, 0, len(keys))
	for _, specifier := range keys {
		entries = append(entries, importMapEntry{Specifier: specifier, URL: entryMap[specifier]})
	}

	payload, err := json.Marshal(clientImportMap{Imports: entryMap})
	if err != nil {
		return nil, "", ""
	}
	sum := sha256.Sum256(payload)
	return entries, `<script type="importmap">` + string(payload) + `</script>`, "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

type importMapEntry struct {
	Specifier string
	URL       string
}

func importMapEntriesVarDecl(name string, entries []importMapEntry) ast.Decl {
	elts := make([]ast.Expr, 0, len(entries))
	entryType := &ast.ArrayType{Len: &ast.BasicLit{Kind: token.INT, Value: "2"}, Elt: ast.NewIdent("string")}
	for _, entry := range entries {
		elts = append(elts, &ast.CompositeLit{
			Type: entryType,
			Elts: []ast.Expr{strLit(entry.Specifier), strLit(entry.URL)},
		})
	}
	return &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(name)},
		Values: []ast.Expr{&ast.CompositeLit{
			Type: &ast.ArrayType{Elt: entryType},
			Elts: elts,
		}},
	}}}
}

func byteSliceVarDecl(name string, value string) ast.Decl {
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{ast.NewIdent(name)},
				Values: []ast.Expr{
					byteSliceConversion(&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}),
				},
			},
		},
	}
}

type clientImportMap struct {
	Imports map[string]string `json:"imports"`
}

func (m *clientImportMap) Add(specifier string, url string) {
	specifier = strings.TrimSpace(specifier)
	url = strings.TrimSpace(url)
	if specifier == "" || url == "" {
		return
	}
	if m.Imports == nil {
		m.Imports = map[string]string{}
	}
	m.Imports[specifier] = url
}

func (m clientImportMap) HTML() string {
	if len(m.Imports) == 0 {
		return ""
	}
	data, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return `<script type="importmap">` + string(data) + `</script>`
}

func buildActionsMetadataScript(signatures map[string][]string) string {
	if len(signatures) == 0 {
		return ""
	}
	data, err := json.Marshal(signatures)
	if err != nil {
		return ""
	}
	return `<script id="_GOX-ACTIONS" type="application/json">` + string(data) + `</script>`
}

func buildClientImportMapEntries(actionsRuntimeURL string, extra map[string]string) string {
	entries := map[string]string{}
	actionsRuntimeURL = strings.TrimSpace(actionsRuntimeURL)
	if actionsRuntimeURL != "" {
		entries["@gox/actions"] = actionsRuntimeURL
	}
	for specifier, url := range extra {
		specifier = strings.TrimSpace(specifier)
		url = strings.TrimSpace(url)
		if specifier == "" || url == "" {
			continue
		}
		entries[specifier] = url
	}
	if len(entries) == 0 {
		return ""
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	if len(data) < 2 {
		return ""
	}
	return string(data[1 : len(data)-1])
}

func buildActionsImportMapEntries(actionsRuntimeURL string) string {
	return buildClientImportMapEntries(actionsRuntimeURL, nil)
}

func bundleActionSignatures(bundle *Bundle) map[string][]string {
	if bundle == nil {
		return nil
	}
	names := bundleActionNames(bundle)
	if len(names) == 0 {
		return nil
	}
	byName := map[string][]string{}
	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind != FuncAction || strings.TrimSpace(fn.Name) == "" {
				continue
			}
			if _, ok := byName[fn.Name]; ok {
				continue
			}
			params := actionUserParams(fn)
			paramNames := make([]string, 0, len(params))
			for _, param := range params {
				paramNames = append(paramNames, param.Name)
			}
			byName[fn.Name] = paramNames
		}
	}
	out := make(map[string][]string, len(names))
	for _, name := range names {
		out[name] = byName[name]
	}
	return out
}

func bundleActionNames(bundle *Bundle) []string {
	if bundle == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind != FuncAction || strings.TrimSpace(fn.Name) == "" {
				continue
			}
			if _, ok := seen[fn.Name]; ok {
				continue
			}
			seen[fn.Name] = struct{}{}
			out = append(out, fn.Name)
		}
	}
	sort.Strings(out)
	return out
}
