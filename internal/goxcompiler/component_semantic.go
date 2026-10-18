package gox

import (
	"go/ast"
	"strings"
)

// ComponentContract is the source-level semantic contract of one local GOX
// component. Props preserve declaration order so later lowering phases can
// bind named template attributes to Go call arguments without re-deriving the
// function signature.
type ComponentContract struct {
	Name         string
	Props        []ComponentPropContract
	Children     *ComponentPropContract
	ChildrenLast bool
}

// ComponentPropContract describes one named component parameter.
type ComponentPropContract struct {
	Name string
	Type ast.Expr
}

// ValidateLocalComponentSemantics validates the named prop contract of local
// GOX components. It deliberately does not lower/reorder arguments (phase 6),
// generate children callbacks (phase 7), or resolve imported components
// (phase 8).
func ValidateLocalComponentSemantics(mod *Module, templates []TemplateReturn) error {
	contracts := localComponentContracts(mod)
	if len(contracts) == 0 {
		return nil
	}

	for _, template := range templates {
		if err := validateLocalComponentNode(template.Template.Root, contracts); err != nil {
			return err
		}
	}
	return nil
}

func localComponentContracts(mod *Module) map[string]ComponentContract {
	if mod == nil || mod.File == nil {
		return nil
	}

	contracts := make(map[string]ComponentContract)
	for _, fn := range mod.Funcs {
		if fn.Kind != FuncComponent || strings.TrimSpace(fn.Name) == "" {
			continue
		}

		contract := ComponentContract{Name: fn.Name, ChildrenLast: true}
		for i, param := range fn.Params {
			if strings.TrimSpace(param.Name) == "" {
				continue
			}
			prop := ComponentPropContract{Name: param.Name, Type: param.Type}
			if isComponentChildrenParameter(mod.File, param.Name, param.Type) {
				copy := prop
				contract.Children = &copy
				contract.ChildrenLast = i == len(fn.Params)-1
				continue
			}
			contract.Props = append(contract.Props, prop)
		}
		contracts[fn.Name] = contract
	}
	return contracts
}

func isComponentChildrenParameter(file *ast.File, name string, typ ast.Expr) bool {
	if name != "children" {
		return false
	}
	path, typeName, ok := goxSourceTypeIdentity(file, typ)
	return ok && path == goxSDKImportPath && typeName == "Component"
}

func validateLocalComponentNode(node TemplateNode, contracts map[string]ComponentContract) error {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *FragmentNode:
		return validateLocalComponentChildren(n.Children, contracts)
	case *ElementNode:
		return validateLocalComponentChildren(n.Children, contracts)
	case *DirectiveNode:
		return validateLocalComponentChildren(n.Children, contracts)
	case *ClientNode:
		return validateLocalComponentChildren(n.Children, contracts)
	case *ComponentNode:
		if err := validateLocalComponentInvocation(n, contracts); err != nil {
			return err
		}
		return validateLocalComponentChildren(n.Children, contracts)
	default:
		return nil
	}
}

func validateLocalComponentChildren(children []TemplateNode, contracts map[string]ComponentContract) error {
	for _, child := range children {
		if err := validateLocalComponentNode(child, contracts); err != nil {
			return err
		}
	}
	return nil
}

func validateLocalComponentInvocation(node *ComponentNode, contracts map[string]ComponentContract) error {
	if node == nil {
		return nil
	}
	contract, ok := contracts[node.Name]
	if !ok {
		// Undefined/non-component and qualified/imported references are owned by
		// the existing reference validator and phase 8 respectively.
		return nil
	}

	if contract.Children != nil && !contract.ChildrenLast {
		return newTemplateErrorfWithHint(
			"children-param-must-be-last",
			node.Pos,
			"Move children to the final parameter of the component signature.",
			"component %s must declare children as its last parameter",
			node.Name,
		)
	}

	props := make(map[string]ComponentPropContract, len(contract.Props))
	for _, prop := range contract.Props {
		props[prop.Name] = prop
	}

	seen := make(map[string]struct{}, len(node.Attrs))
	for _, attr := range node.Attrs {
		if _, exists := seen[attr.Name]; exists {
			return newTemplateErrorfWithHint(
				"duplicate-component-prop",
				attr.Pos,
				"Remove the duplicate prop so each component prop is provided once.",
				"component %s prop %q is provided more than once",
				node.Name,
				attr.Name,
			)
		}
		seen[attr.Name] = struct{}{}

		if _, exists := props[attr.Name]; !exists {
			return newTemplateErrorfWithHint(
				"unknown-component-prop",
				attr.Pos,
				"Use a parameter declared by the component function.",
				"component %s has no prop named %q",
				node.Name,
				attr.Name,
			)
		}
	}

	for _, prop := range contract.Props {
		if _, exists := seen[prop.Name]; exists {
			continue
		}
		return newTemplateErrorfWithHint(
			"missing-component-prop",
			node.Pos,
			"Provide every required component prop by name.",
			"component %s requires prop %q",
			node.Name,
			prop.Name,
		)
	}

	if !node.SelfClosed && contract.Children == nil {
		return newTemplateErrorfWithHint(
			"component-does-not-accept-children",
			node.Pos,
			"Add a final children gox.Component parameter or make the component self-closing.",
			"component %s does not accept children",
			node.Name,
		)
	}

	return nil
}
