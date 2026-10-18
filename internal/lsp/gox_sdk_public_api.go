package lsp

import (
	"fmt"
	"sort"
	"strings"
)

const goxSDKImportPath = "github.com/gox-lang/gox"

type goxSDKAPIGroup struct {
	title string
	items []string
}

func isGoxSDKPackage(importPath string) bool {
	return importPath == goxSDKImportPath
}

func goxSDKImportMarkup(packageInfo GoPackage, path, alias string, explicit bool) MarkupContent {
	groups := goxSDKPublicAPIGroups(packageInfo)
	var value strings.Builder
	value.WriteString("**GOX SDK public API**\n\n")
	value.WriteString("```gox\npackage ")
	value.WriteString(packageInfo.Name)
	value.WriteString("\n```")
	for _, group := range groups {
		if len(group.items) == 0 {
			continue
		}
		value.WriteString("\n\n**")
		value.WriteString(group.title)
		value.WriteString("**\n\n```gox\n")
		value.WriteString(strings.Join(group.items, "\n"))
		value.WriteString("\n```")
	}
	aliasKind := "inferred"
	if explicit {
		aliasKind = "explicit"
	}
	value.WriteString(fmt.Sprintf("\n\nGOX SDK imported with %s alias `%s` from `%s`.", aliasKind, alias, path))
	return MarkupContent{Kind: "markdown", Value: value.String()}
}

func goxSDKPublicAPIGroups(packageInfo GoPackage) []goxSDKAPIGroup {
	groups := []goxSDKAPIGroup{
		{title: "Types"},
		{title: "Functions"},
		{title: "Variables"},
		{title: "Constants"},
	}
	for _, exported := range packageInfo.Exports {
		detail := exported.Detail
		switch exported.Kind {
		case "struct", "interface":
			detail = exported.Kind + " " + exported.Name
			groups[0].items = append(groups[0].items, detail)
		case "type":
			groups[0].items = append(groups[0].items, detail)
		case "func":
			groups[1].items = append(groups[1].items, detail)
		case "var":
			groups[2].items = append(groups[2].items, detail)
		case "const":
			groups[3].items = append(groups[3].items, detail)
		}
	}
	for index := range groups {
		sort.Strings(groups[index].items)
	}
	return groups
}
