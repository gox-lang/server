package gox

import (
	"fmt"
	"go/ast"
)

// The lexical pass produces a parseable callback before function signatures
// are known. Generate its runtime body from the retained template once the
// module's component contracts and value environment are available.
func rewriteInlineTemplateValues(mod *Module, occurrences []TemplateOccurrence, base *TemplateTypeEnv) ([]TemplateStaticDecl, error) {
	byOffset := map[int]TemplateOccurrence{}
	for _, occurrence := range occurrences {
		byOffset[occurrence.Source.Range.Start] = occurrence
	}
	var statics []TemplateStaticDecl
	var generationErr error
	clients := map[string]ClientDecl{}
	for _, client := range mod.Clients {
		clients[client.Name] = client
	}
	for _, decl := range mod.File.Decls {
		env := base
		if fn, ok := decl.(*ast.FuncDecl); ok {
			env = templateTypeEnvForFunc(fn, base)
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			if generationErr != nil {
				return false
			}
			literal, ok := node.(*ast.FuncLit)
			if !ok {
				return true
			}
			origin, ok := mod.Origins.Get(literal)
			if !ok {
				return true
			}
			occurrence, ok := byOffset[origin.Source.Range.Start]
			if !ok || origin.Source != occurrence.Source {
				return true
			}
			var literals []string
			// Earlier lexical replacements can change line counts. Runtime
			// diagnostics must use the original GOX coordinates, not that text.
			line, column := sourceLineColumn(mod.Source, occurrence.Content.Range.Start)
			template, err := ParseTemplateAtOffset(occurrence.Raw, mod.Path, line, column, occurrence.Content.Range.Start)
			if err != nil {
				generationErr = err
				return false
			}
			statements, err := GenerateTemplateStmts(template, &TemplateGenContext{
				WriterName: "goxWriter", StaticVarName: occurrence.StaticName,
				StaticLiterals: &literals, TypeEnv: env, Clients: clients,
				InjectClientRuntime: len(clients) > 0,
			})
			if err != nil {
				generationErr = err
				return false
			}
			literal.Body.List = statements
			for _, stmt := range statements {
				mod.Origins.InheritTree(stmt, literal)
			}
			if len(literals) > 0 {
				statics = append(statics, TemplateStaticDecl{Name: occurrence.StaticName, Literals: literals})
			}
			delete(byOffset, origin.Source.Range.Start)
			return false
		})
	}
	if generationErr == nil && len(byOffset) != 0 {
		return nil, fmt.Errorf("cannot locate %d inline template callbacks in %s", len(byOffset), mod.Path)
	}
	return statics, generationErr
}
