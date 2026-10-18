package lsp

import (
	"fmt"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) importedGoPackageHover(document Document, position Position, offset int) *Hover {
	identifier, _, err := identifierAtPosition(document, position)
	if err != nil || identifier == "" {
		return nil
	}
	selector, hasSelector := selectorAtOffset(document.Text, offset)
	lookupAlias := identifier
	if hasSelector && selector.cursorOnMember {
		lookupAlias = selector.qualifier
	}
	declaration, packageInfo, alias, explicit, ok := s.resolveGoPackageImport(document, lookupAlias)
	if !ok {
		return nil
	}
	if hasSelector && selector.cursorOnMember && selector.qualifier == alias {
		for _, exported := range packageInfo.Exports {
			if exported.Name == selector.member {
				return hoverForDetail(exported.Detail, packageInfo.Path)
			}
		}
		return nil
	}
	if identifier != alias {
		return nil
	}
	return &Hover{Contents: goPackageImportMarkup(packageInfo, declaration.Path, alias, explicit)}
}

func goPackageImportMarkup(packageInfo GoPackage, path, alias string, explicit bool) MarkupContent {
	if isGoxSDKPackage(packageInfo.Path) {
		return goxSDKImportMarkup(packageInfo, path, alias, explicit)
	}
	detail, hidden := goPackageSummary(packageInfo)
	value := "```gox\n" + detail + "\n```"
	aliasKind := "inferred"
	if explicit {
		aliasKind = "explicit"
	}
	value += fmt.Sprintf("\n\nGo package imported with %s alias `%s` from `%s`.", aliasKind, alias, path)
	if hidden > 0 {
		value += fmt.Sprintf("\n\n%d additional exported declarations not shown.", hidden)
	}
	return MarkupContent{Kind: "markdown", Value: value}
}

func (s *Server) resolveGoPackageImport(document Document, identifier string) (goxcompiler.ImportDecl, GoPackage, string, bool, bool) {
	root := s.projectRoot(document.Path)
	for _, declaration := range s.symbols.GoImports(document.URI) {
		if declaration.Alias == "_" || declaration.Alias == "." {
			continue
		}
		if declaration.Alias != "" && declaration.Alias != identifier {
			continue
		}
		packageInfo, err := s.goPackages.Load(root, declaration.Path)
		if err != nil {
			continue
		}
		alias := declaration.Alias
		explicit := alias != ""
		if alias == "" {
			alias = packageInfo.Name
		}
		if alias == identifier {
			return declaration, packageInfo, alias, explicit, true
		}
	}
	return goxcompiler.ImportDecl{}, GoPackage{}, "", false, false
}

func goPackageSummary(packageInfo GoPackage) (string, int) {
	lines := make([]string, 0, importedNamespaceExportLimit)
	for _, exported := range packageInfo.Exports {
		detail := exported.Detail
		if exported.Kind == "struct" || exported.Kind == "interface" {
			detail = exported.Kind + " " + exported.Name
		}
		lines = append(lines, detail)
	}
	hidden := 0
	if len(lines) > importedNamespaceExportLimit {
		hidden = len(lines) - importedNamespaceExportLimit
		lines = lines[:importedNamespaceExportLimit]
	}
	if len(lines) == 0 {
		return "package " + packageInfo.Name + " {}", hidden
	}
	return "package " + packageInfo.Name + " {\n\t" + strings.Join(lines, "\n\t") + "\n}", hidden
}
