package lsp

import (
	"go/types"
	"strings"
)

func goObjectDetail(object types.Object, qualifier types.Qualifier) string {
	switch value := object.(type) {
	case *types.TypeName:
		return goTypeNameDetail(value, qualifier)
	case *types.Const:
		return types.ObjectString(value, qualifier) + " = " + value.Val().ExactString()
	default:
		return types.ObjectString(object, qualifier)
	}
}

func goTypeNameDetail(object *types.TypeName, qualifier types.Qualifier) string {
	if object.IsAlias() {
		if alias, ok := object.Type().(*types.Alias); ok {
			return "type " + object.Name() + " = " + types.TypeString(alias.Rhs(), qualifier)
		}
	}
	switch underlying := object.Type().Underlying().(type) {
	case *types.Struct:
		return goStructDetail(object.Name(), underlying, qualifier)
	case *types.Interface:
		return goInterfaceDetail(object.Name(), underlying, qualifier)
	default:
		return "type " + object.Name() + " " + types.TypeString(underlying, qualifier)
	}
}

func goStructDetail(name string, structure *types.Struct, qualifier types.Qualifier) string {
	lines := make([]string, 0, structure.NumFields()+1)
	filtered := false
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() {
			filtered = true
			continue
		}
		line := field.Name() + " " + types.TypeString(field.Type(), qualifier)
		if field.Embedded() {
			line = types.TypeString(field.Type(), qualifier)
		}
		if tag := structure.Tag(index); tag != "" {
			line += " `" + tag + "`"
		}
		lines = append(lines, line)
	}
	if filtered {
		lines = append(lines, "// contains filtered or unexported fields")
	}
	return goBlockTypeDetail("type "+name+" struct", lines)
}

func goInterfaceDetail(name string, contract *types.Interface, qualifier types.Qualifier) string {
	contract.Complete()
	lines := make([]string, 0, contract.NumExplicitMethods()+contract.NumEmbeddeds()+1)
	filtered := false
	for index := 0; index < contract.NumEmbeddeds(); index++ {
		embedded := contract.EmbeddedType(index)
		if !goTypeIsExported(embedded) {
			filtered = true
			continue
		}
		lines = append(lines, types.TypeString(embedded, qualifier))
	}
	for index := 0; index < contract.NumExplicitMethods(); index++ {
		method := contract.ExplicitMethod(index)
		if !method.Exported() {
			filtered = true
			continue
		}
		signature := strings.TrimPrefix(types.TypeString(method.Type(), qualifier), "func")
		lines = append(lines, method.Name()+signature)
	}
	if filtered {
		lines = append(lines, "// contains filtered or unexported methods")
	}
	return goBlockTypeDetail("type "+name+" interface", lines)
}

func goBlockTypeDetail(header string, lines []string) string {
	if len(lines) == 0 {
		return header + "{}"
	}
	return header + " {\n\t" + strings.Join(lines, "\n\t") + "\n}"
}

func goTypeIsExported(value types.Type) bool {
	switch item := value.(type) {
	case *types.Pointer:
		return goTypeIsExported(item.Elem())
	case *types.Named:
		return item.Obj().Exported()
	case *types.Alias:
		return item.Obj().Exported()
	default:
		return true
	}
}
