package gox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
	"github.com/gox-lang/server/internal/goxsource"
)

func ParseModule(path string, src string) (*Module, error) {
	return parseModuleWithContext(path, src, goxsource.ClassifyModule(filepath.ToSlash(path)))
}

// ParseModuleForTooling parses a GOX source for editor intelligence. It keeps
// structural and declaration semantics, but tolerates incomplete component
// invocations so completion can operate while required props are still being
// typed. Compiler entry points must continue using ParseModule.
func ParseModuleForTooling(path string, src string) (*Module, error) {
	return parseModuleWithSemanticContextMode(path, src, goxsource.ClassifyModule(filepath.ToSlash(path)), nil, nil, false)
}

// ParseProjectModule parses a GOX source using its path relative to the project
// root as the structural authority. Development tooling should prefer this
// when the project root is known so absolute directory names cannot affect the
// pages/websocket classification.
func ParseProjectModule(root, path, src string) (*Module, error) {
	return ParseProjectModuleCached(root, path, src, nil)
}

// ParseProjectModuleForTooling is the project-aware tooling variant of
// ParseModuleForTooling.
func ParseProjectModuleForTooling(root, path, src string) (*Module, error) {
	return ParseProjectModuleCachedForTooling(root, path, src, nil)
}

// ParseProjectModuleCached is the incremental-tooling variant of
// ParseProjectModule. The optional cache reuses immutable Go package type
// identities across document versions while every GOX source projection is
// still rebuilt from its current source.
func ParseProjectModuleCached(root, path, src string, cache *SemanticWorkspaceCache) (*Module, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	return parseModuleWithSemanticWorkspace(path, src, goxsource.ClassifyModule(filepath.ToSlash(rel)), newSemanticWorkspace(root, cache))
}

// ParseProjectModuleCachedForTooling is the incremental editor variant of
// ParseProjectModuleCached. Component invocation completeness is intentionally
// not a parse blocker; the strict compiler path still validates it.
func ParseProjectModuleCachedForTooling(root, path, src string, cache *SemanticWorkspaceCache) (*Module, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	return parseModuleWithSemanticContextMode(path, src, goxsource.ClassifyModule(filepath.ToSlash(rel)), newSemanticWorkspace(root, cache), nil, false)
}

func parseModuleWithContext(path string, src string, structure goxsource.ModuleContext) (*Module, error) {
	return parseModuleWithSemanticContext(path, src, structure, nil, nil)
}

// parseModuleWithImporter is the compiler hot-path variant. One importer can be
// shared by all modules in a single LoadGraph call, allowing go/importer to
// reuse decoded export data and canonical *types.Package identities without
// leaking semantic state across independent compiles.
func parseModuleWithImporter(path string, src string, structure goxsource.ModuleContext, semanticImporter types.Importer) (*Module, error) {
	return parseModuleWithSemanticContext(path, src, structure, nil, semanticImporter)
}

func parseModuleWithSemanticWorkspace(path string, src string, structure goxsource.ModuleContext, workspace *semanticWorkspace) (*Module, error) {
	return parseModuleWithSemanticContext(path, src, structure, workspace, nil)
}

func parseModuleWithSemanticContext(path string, src string, structure goxsource.ModuleContext, workspace *semanticWorkspace, semanticImporter types.Importer) (*Module, error) {
	return parseModuleWithSemanticContextMode(path, src, structure, workspace, semanticImporter, true)
}

func parseModuleWithSemanticContextMode(path string, src string, structure goxsource.ModuleContext, workspace *semanticWorkspace, semanticImporter types.Importer, validateComponentInvocations bool) (*Module, error) {
	originalSource := src
	declaredModuleName, src, sourceMap, err := extractModuleDeclMapped(path, src)
	if err != nil {
		return nil, err
	}

	moduleName := normalizeModuleName(declaredModuleName)

	clients, src, sourceMap, err := extractClientDeclsMapped(path, src, sourceMap)
	if err != nil {
		return nil, err
	}
	for i := range clients {
		clients[i].Key = ClientDeclarationKey(path, clients[i].Name, strings.ToLower(string(clients[i].Framework)), clients[i].Entry, clients[i].Dev)
	}

	extracted, err := ExtractTemplateReturnsMapped(moduleName, path, src, sourceMap)
	if err != nil {
		return nil, err
	}

	transformed, err := TransformTemplateExpressionsMapped(moduleName, path, extracted.Source, extracted.SourceMap)
	if err != nil {
		return nil, err
	}
	transformedSource := transformed.Source
	componentExpressionStatics := transformed.Statics

	parseWriter, err := goxmap.NewWriter(path+"#parse", transformed.SourceMap.GeneratedURI(), transformedSource)
	if err != nil {
		return nil, err
	}
	if err := parseWriter.WriteGenerated("package main\n", goxmap.Range{Start: 0, End: 0}); err != nil {
		return nil, err
	}
	if err := parseWriter.WriteSource(goxmap.Range{Start: 0, End: len(transformedSource)}); err != nil {
		return nil, err
	}
	goSrc, parseStageMap, err := parseWriter.Build()
	if err != nil {
		return nil, err
	}
	parseSourceMap, err := goxmap.Compose(path+"#parse", parseStageMap, transformed.SourceMap)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, goSrc, parser.ParseComments)
	if err != nil {
		return nil, mapGoSyntaxError(err, path, originalSource, goSrc, parseSourceMap)
	}
	origins := SeedOriginTable(file, fset, parseSourceMap)
	goSemanticTokens := collectGoSemanticTokens(originalSource, file, origins)
	goSymbols := collectGoSourceSymbols(file, origins)
	templateOccurrences := make([]TemplateOccurrence, 0, len(extracted.Templates)+len(transformed.Occurrences))
	for _, template := range extracted.Templates {
		templateOccurrences = append(templateOccurrences, template.Occurrence())
	}
	templateOccurrences = append(templateOccurrences, transformed.Occurrences...)
	sort.SliceStable(templateOccurrences, func(i, j int) bool {
		return templateOccurrences[i].Source.Range.Start < templateOccurrences[j].Source.Range.Start
	})

	semanticProjection, err := buildSemanticProjectionFromOccurrences(path, src, sourceMap, templateOccurrences)
	if err != nil {
		return nil, err
	}
	var semantic *SemanticModel
	switch {
	case workspace != nil:
		semantic = workspace.buildModel(path, semanticProjection)
	case semanticImporter != nil:
		semantic = buildSemanticModelWithImporter(
			semanticProjection.File,
			semanticProjection.FileSet,
			semanticProjection.Origins,
			semanticProjection.SourceMap,
			"",
			semanticImporter,
		)
	default:
		semantic = buildSemanticModelFromProjection(semanticProjection)
	}
	if semantic == nil {
		return nil, fmt.Errorf("build semantic model for %s", path)
	}
	localSymbols, selectorReferences, unresolvedIdentifiers := collectLocalSymbols(semantic.File, semantic.FileSet, semantic.SourceMap, semantic.Origins, semantic)
	localSymbols, selectorReferences, unresolvedIdentifiers = keepLegacySemanticIndexesOutsideTemplates(localSymbols, selectorReferences, unresolvedIdentifiers, templateOccurrences)

	if err := validateClientTopLevelNames(path, src, fset, file, clients); err != nil {
		return nil, err
	}

	goxImports, err := TransformModuleImports(file, origins)
	if err != nil {
		return nil, err
	}

	canonicalizeGoxSDKRuntimeImport(file)

	mod := &Module{
		GoxImports:            goxImports,
		Path:                  path,
		Name:                  moduleName,
		DeclaredName:          declaredModuleName,
		GoPackage:             "main",
		Structure:             structure,
		Templates:             extracted.Templates,
		TemplateOccurrences:   templateOccurrences,
		Clients:               clients,
		File:                  file,
		Source:                originalSource,
		ParseSource:           goSrc,
		ParseSourceMap:        parseSourceMap,
		Origins:               origins,
		Semantic:              semantic,
		GoSemanticTokens:      goSemanticTokens,
		Symbols:               goSymbols,
		LocalSymbols:          localSymbols,
		SelectorReferences:    selectorReferences,
		UnresolvedIdentifiers: unresolvedIdentifiers,
	}
	mod.LocalSymbols = append(mod.LocalSymbols, collectTemplateLocalSymbols(originalSource, file, mod.Templates)...)
	mod.Symbols = append(mod.Symbols, collectNativeSourceSymbols(originalSource, mod)...)
	mod.ClientSemantics = collectClientSemantics(originalSource, mod)
	mod.References = collectTemplateSourceReferences(originalSource, path, mod.Templates)

	// Captura funcs originales antes de transformar signatures/returns.
	if err := analyzeFileDecls(mod, file); err != nil {
		return nil, err
	}

	if err := ValidateModuleStructure(mod); err != nil {
		return nil, err
	}

	if err := ValidatePageEntrypoints(mod); err != nil {
		return nil, err
	}

	if err := ValidateActions(mod); err != nil {
		return nil, err
	}

	if err := ValidateWebSocketEntrypoints(mod); err != nil {
		return nil, err
	}
	if err := ValidateChannelEntrypoints(mod); err != nil {
		return nil, err
	}

	if err := validateGoroutineSafety(path, originalSource, goSrc, fset, parseSourceMap, file); err != nil {
		return nil, err
	}

	mod.WebSocketContracts = collectWebSocketContracts(mod)

	if err := RewriteWebSocketOnSignaturesWithOrigins(mod, fset, mod.Origins); err != nil {
		return nil, err
	}
	RewriteWebSocketEmitAwaitWithOrigins(mod, mod.Origins)

	if err := ValidateTemplateComponentReferences(mod, extracted.Templates); err != nil {
		return nil, err
	}
	if validateComponentInvocations {
		if err := ValidateLocalComponentSemantics(mod, extracted.Templates); err != nil {
			return nil, err
		}
	}

	templateEnv := buildTemplateBaseTypeEnv(file)
	templateEnv.ComponentCalls = &mod.ComponentCalls
	templateEnv.StrictComponents = validateComponentInvocations
	componentExpressionStatics, err = rewriteInlineTemplateValues(mod, transformed.Occurrences, templateEnv)
	if err != nil {
		return nil, err
	}
	templateStatics, err := RewriteTemplateReturnsWithOrigins(file, extracted.Templates, clients, mod.Origins, templateEnv)
	if err != nil {
		return nil, err
	}

	AddClientMethods(file, moduleName, clients)

	if err := TransformGoxFunctionsWithOrigins(file, mod.Origins); err != nil {
		return nil, err
	}

	RenameActionFunctions(file, mod)

	if err := EnsureActionReturns(file, fset, mod); err != nil {
		return nil, err
	}
	if err := TransformFunctionsToMethodsWithOrigins(file, moduleName, mod.Origins); err != nil {
		return nil, err
	}

	if err := BindModuleMethodCallsWithOrigins(file, "m", mod.Origins); err != nil {
		return nil, err
	}

	consts, err := TransformModuleConstsWithOrigins(file, moduleName, mod.Origins)
	if err != nil {
		return nil, err
	}

	types, err := TransformModuleTypesWithOrigins(file, moduleName, mod.Origins)
	if err != nil {
		return nil, err
	}

	fields, err := CollectModuleFieldsWithOrigins(file, moduleName, mod.Origins)
	if err != nil {
		return nil, err
	}

	allTemplateStatics := append(templateStatics, componentExpressionStatics...)
	AddTemplateStaticDeclsWithOrigins(file, allTemplateStatics, mod.Origins)

	if err := BindModuleFieldIdentifiersWithOrigins(file, fields, "m", mod.Origins); err != nil {
		return nil, err
	}

	if err := AddModuleConstructorWithOrigins(file, moduleName, fields, mod.Origins); err != nil {
		return nil, err
	}

	CleanupRedundantReturns(file)

	anchorTemplateGeneratedStatements(file, mod.Origins, path, originalSource)
	AddUserStatementLineMarkers(file, fset, path)

	mod.Vars = fields
	mod.Consts = consts
	mod.Types = types

	if err := analyzeFileImports(mod, file, fset); err != nil {
		return nil, err
	}

	return mod, nil
}

func analyzeFileDecls(mod *Module, file *ast.File) error {
	mod.Funcs = nil

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		params := []flatParam{}

		if fn.Type != nil && fn.Type.Params != nil {
			params = flattenFuncParams(fn.Type.Params)
		}

		kind := detectFuncKindFromReturn(file, fn)
		meta := FuncDecl{
			Name:   fn.Name.Name,
			Kind:   kind,
			Node:   fn,
			Params: params,
		}
		if kind == FuncAction {
			env := templateTypeEnvForFunc(fn, buildTemplateBaseTypeEnv(file))
			meta.ActionReturnTypes = collectActionReturnTypeNames(fn, env)
		}
		mod.Funcs = append(mod.Funcs, meta)
	}

	return nil
}

func detectFuncKindFromReturn(file *ast.File, fn *ast.FuncDecl) FuncKind {
	if fn.Type == nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return FuncNormal
	}

	resultType := fn.Type.Results.List[0].Type
	path, name, ok := goxSourceTypeIdentity(file, resultType)
	if !ok {
		return FuncNormal
	}
	if path == goxServerImportPath && name == "Config" {
		return FuncConfig
	}
	if path != goxSDKImportPath {
		return FuncNormal
	}

	switch name {
	case "Page":
		return FuncPage
	case "Component":
		return FuncComponent
	case "Action":
		return FuncAction
	case "WebSocket":
		return FuncWebSocket
	case "Channel":
		return FuncChannel
	default:
		return FuncNormal
	}
}

// =========================
// MODULE DECL
// =========================

func extractModuleDecl(src string) (string, string, error) {
	name, transformed, _, err := extractModuleDeclMapped("gox:source", src)
	return name, transformed, err
}

func extractModuleDeclMapped(path string, src string) (string, string, *goxmap.Map, error) {
	for lineStart := 0; lineStart <= len(src); {
		lineEnd := strings.IndexByte(src[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(src)
		} else {
			lineEnd += lineStart
		}
		line := src[lineStart:lineEnd]
		trim := strings.TrimSpace(line)

		if trim == "" || strings.HasPrefix(trim, "//") {
			if lineEnd == len(src) {
				break
			}
			lineStart = lineEnd + 1
			continue
		}

		if !strings.HasPrefix(trim, "module ") {
			return "", "", nil, newCompilerErrorWithHint("missing-module-declaration", "first declaration must be module <Name>", "Start the file with module <Name> before imports or declarations.")
		}

		name := strings.TrimSpace(strings.TrimPrefix(trim, "module "))
		if name == "" {
			return "", "", nil, newCompilerErrorWithHint("missing-module-name", "missing module name", "Use module <Name>, for example: module main.")
		}

		if !isValidIdent(name) {
			return "", "", nil, newCompilerErrorWithHint("invalid-module-name", fmt.Sprintf("invalid module name %q", name), "Use a valid Go identifier as the module name.")
		}

		writer, err := goxmap.NewWriter(path+"#module", path, src)
		if err != nil {
			return "", "", nil, err
		}
		if err := writer.WriteSource(goxmap.Range{Start: 0, End: lineStart}); err != nil {
			return "", "", nil, err
		}
		if err := writer.WriteSource(goxmap.Range{Start: lineEnd, End: len(src)}); err != nil {
			return "", "", nil, err
		}
		transformed, sourceMap, err := writer.Build()
		if err != nil {
			return "", "", nil, err
		}
		return name, transformed, sourceMap, nil
	}

	return "", "", nil, newCompilerErrorWithHint("missing-module-declaration", "missing module declaration", "Start the file with module <Name>.")
}

func isValidIdent(s string) bool {
	if s == "" {
		return false
	}

	for i, r := range s {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}

		if r != '_' &&
			(r < 'A' || r > 'Z') &&
			(r < 'a' || r > 'z') &&
			(r < '0' || r > '9') {
			return false
		}
	}

	return true
}

// =========================
// IMPORTS
// =========================

func analyzeFileImports(mod *Module, file *ast.File, fset *token.FileSet) error {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)

		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}

		switch {
		case strings.HasSuffix(path, ".gox"):
			// mod.GoxImports = append(mod.GoxImports, GoxImport{
			// 	Path: path,
			// 	Name: importNameFromPath(path),
			// })
			continue

		case strings.HasPrefix(path, "@"):
			return newCompilerErrorWithHint(
				"invalid-gox-alias-import",
				fmt.Sprintf("alias import %q must include the .gox extension", path),
				"Use an explicit .gox alias, for example: import \"@layout.gox\".",
			)

		case strings.HasSuffix(path, ".css"):
			mod.Assets = append(mod.Assets, AssetImport{
				Path:  path,
				Alias: alias,
				Kind:  AssetCSS,
				Spec:  imp,
			})

		default:
			sourceLine := 0
			if fset != nil {
				pos := fset.Position(imp.Pos())
				sourceLine = pos.Line - 1
				if sourceLine < 1 {
					sourceLine = pos.Line
				}
			}

			declaration := ImportDecl{
				Path:       path,
				Alias:      alias,
				Spec:       imp,
				SourceFile: mod.Path,
				SourceLine: sourceLine,
			}
			if source, ok := exactSourceRange(mod.Origins, imp.Path); ok {
				declaration.Source = source
			}
			mod.GoImports = append(mod.GoImports, declaration)
		}
	}

	return nil
}

func importNameFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	if base == "" {
		return ""
	}

	return toPascalCase(base)
}

func toPascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})

	var out strings.Builder

	for _, p := range parts {
		if p == "" {
			continue
		}

		out.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			out.WriteString(p[1:])
		}
	}

	return out.String()
}
