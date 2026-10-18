package gox

import (
	"go/ast"
	"strings"
)

// Keep the source contract alongside the generated call until the imported
// module is available. Argument expressions are taken from Call after binding
// passes, so receiver and module-field rewrites are preserved.
type pendingComponentCall struct {
	Node  *ComponentNode
	Call  *ast.CallExpr
	Bound bool
}

func bindImportedComponentCalls(graph *ModuleGraph) error {
	for _, mod := range graph.Modules {
		contracts := map[string]ComponentContract{}
		imports := map[string]bool{}
		for _, imp := range mod.GoxImports {
			child := graph.ByPath[imp.ResolvedPath]
			if child == nil {
				continue
			}
			importName := imp.Name
			if importName == "" {
				importName = child.Name
			}
			imports[importName] = true
			for name, contract := range localComponentContracts(child) {
				contracts[importName+"."+name] = contract
			}
		}
		for i := range mod.ComponentCalls {
			pending := &mod.ComponentCalls[i]
			if pending.Bound {
				continue
			}
			node, call := pending.Node, pending.Call
			qualifier, _, _ := strings.Cut(node.Name, ".")
			if !imports[qualifier] {
				continue
			}
			contract, ok := contracts[node.Name]
			if !ok {
				return newTemplateErrorfWithHint("invalid-imported-component", node.Pos, "Use a component declared by the imported GOX module.", "%s is not an imported component", node.Name)
			}
			if err := validateLocalComponentInvocation(node, contracts); err != nil {
				return err
			}
			args := []ast.Expr{call.Args[0]}
			for _, prop := range contract.Props {
				for index, attr := range node.Attrs {
					if attr.Name == prop.Name {
						args = append(args, call.Args[index+1])
						break
					}
				}
			}
			if contract.Children != nil {
				if len(node.Children) > 0 {
					args = append(args, call.Args[len(call.Args)-1])
				} else {
					absent := ast.NewIdent("nil")
					mod.Origins.Inherit(absent, call)
					args = append(args, absent)
				}
			}
			call.Args = args
			pending.Bound = true
		}
	}
	return nil
}
