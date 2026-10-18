package gox

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type actionTypeParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type actionTypeDecl struct {
	Source  string            `json:"source"`
	Name    string            `json:"name"`
	Params  []actionTypeParam `json:"params,omitempty"`
	Results []string          `json:"results"`
}
type actionTypeRegistry struct {
	Sources map[string][]actionTypeDecl `json:"sources"`
}

var actionTypeProjectLocks sync.Map

func actionTypeProjectLock(root string) *sync.Mutex {
	key, err := filepath.Abs(root)
	if err != nil {
		key = filepath.Clean(root)
	}
	lock, _ := actionTypeProjectLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// UpdateActionTypesForModule updates the generated Action typings for one GOX
// source. Development tooling can pass a Module parsed from unsaved editor text,
// making that in-memory document the source of truth for the source entry.
func UpdateActionTypesForModule(root string, mod *Module) error {
	if mod == nil || strings.TrimSpace(root) == "" {
		return nil
	}
	return mutateActionTypes(root, func(registry *actionTypeRegistry) {
		source := actionTypeSource(root, mod.Path)
		registry.Sources[source] = collectModuleActionTypes(mod, source)
	})
}

// RemoveActionTypesForSource removes one GOX source from the generated Action
// typings after that source is deleted or renamed.
func RemoveActionTypesForSource(root, sourcePath string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(sourcePath) == "" {
		return nil
	}
	return mutateActionTypes(root, func(registry *actionTypeRegistry) {
		delete(registry.Sources, actionTypeSource(root, sourcePath))
	})
}

// PruneActionTypesSources removes cached Action sources that are not part of
// the supplied project index.
func PruneActionTypesSources(root string, sourcePaths []string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	keep := make(map[string]struct{}, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		if strings.TrimSpace(sourcePath) != "" {
			keep[actionTypeSource(root, sourcePath)] = struct{}{}
		}
	}
	return mutateActionTypes(root, func(registry *actionTypeRegistry) {
		for source := range registry.Sources {
			if _, ok := keep[source]; !ok {
				delete(registry.Sources, source)
			}
		}
	})
}

func mutateActionTypes(root string, mutate func(*actionTypeRegistry)) error {
	lock := actionTypeProjectLock(root)
	lock.Lock()
	defer lock.Unlock()

	buildDir := filepath.Join(root, ".build")
	cacheDir := filepath.Join(buildDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	registryPath := filepath.Join(cacheDir, "actions-types.json")
	registry := actionTypeRegistry{Sources: map[string][]actionTypeDecl{}}
	if data, err := os.ReadFile(registryPath); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &registry)
	}
	if registry.Sources == nil {
		registry.Sources = map[string][]actionTypeDecl{}
	}
	mutate(&registry)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeAtomicIfChanged(registryPath, data); err != nil {
		return err
	}
	return writeAtomicIfChanged(filepath.Join(buildDir, "actions.d.ts"), renderActionTypes(registry))
}

func updateActionTypes(graph *ModuleGraph) error {
	if graph == nil || strings.TrimSpace(graph.RootDir) == "" {
		return nil
	}
	lock := actionTypeProjectLock(graph.RootDir)
	lock.Lock()
	defer lock.Unlock()

	buildDir := filepath.Join(graph.RootDir, ".build")
	cacheDir := filepath.Join(buildDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	registryPath := filepath.Join(cacheDir, "actions-types.json")
	registry := actionTypeRegistry{Sources: map[string][]actionTypeDecl{}}
	if data, err := os.ReadFile(registryPath); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &registry)
	}
	if registry.Sources == nil {
		registry.Sources = map[string][]actionTypeDecl{}
	}
	for source := range registry.Sources {
		if _, err := os.Stat(filepath.Join(graph.RootDir, filepath.FromSlash(source))); os.IsNotExist(err) {
			delete(registry.Sources, source)
		}
	}
	for _, mod := range graph.Modules {
		source := actionTypeSource(graph.RootDir, mod.Path)
		registry.Sources[source] = collectModuleActionTypes(mod, source)
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeAtomicIfChanged(registryPath, data); err != nil {
		return err
	}
	return writeAtomicIfChanged(filepath.Join(buildDir, "actions.d.ts"), renderActionTypes(registry))
}

func actionTypeSource(root, path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func collectModuleActionTypes(mod *Module, source string) []actionTypeDecl {
	out := []actionTypeDecl{}
	for _, fn := range mod.Funcs {
		if fn.Kind != FuncAction || fn.Node == nil {
			continue
		}
		params := []actionTypeParam{}
		for _, p := range actionUserParams(fn) {
			params = append(params, actionTypeParam{Name: p.Name, Type: actionParamTSType(mod, p)})
		}
		results := make([]string, 0, len(fn.ActionReturnTypes))
		for _, typeName := range fn.ActionReturnTypes {
			results = append(results, clientTypeNameTSType(mod, typeName))
		}
		sort.Strings(results)
		out = append(out, actionTypeDecl{Source: source, Name: fn.Name, Params: params, Results: results})
	}
	return out
}

func actionParamTSType(mod *Module, param flatParam) string {
	jsName := actionParamJSName(mod, param)
	switch jsName {
	case "string", "boolean", "number", "Date", "File", "File[]":
		return jsName
	}
	return clientJSONExprTSType(mod, param.Type, map[string]bool{})
}

func clientTypeNameTSType(mod *Module, t string) string {
	t = strings.TrimSpace(t)
	switch t {
	case "nil":
		return "null"
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "uint", "uint8", "uint16", "uint32", "uintptr", "float32", "float64", "byte", "rune":
		return "number"
	case "int64", "uint64", "big.Int", "*big.Int":
		return "bigint"
	case "HTML", "gox.HTML":
		return "Node"
	case "gox.File", "File":
		return "File"
	case "[]gox.File", "[]File":
		return "File[]"
	}
	if expr := actionTypeExpr(mod, t); expr != nil {
		return clientJSONExprTSType(mod, expr, map[string]bool{})
	}
	return "unknown"
}

func actionTypeExpr(mod *Module, name string) ast.Expr {
	name = strings.TrimSpace(name)
	for _, typ := range mod.Types {
		if typ.Node == nil || typ.Node.Name == nil {
			continue
		}
		if name == typ.Name || name == typ.Node.Name.Name {
			return typ.Node.Type
		}
	}
	// Return analysis can preserve composite type text rather than a named type.
	expr, err := parserExpr(name)
	if err == nil {
		return expr
	}
	return nil
}

func parserExpr(src string) (ast.Expr, error) {
	return parser.ParseExpr(src)
}

func clientJSONExprTSType(mod *Module, expr ast.Expr, visiting map[string]bool) string {
	if expr == nil {
		return "unknown"
	}
	switch x := expr.(type) {
	case *ast.Ident:
		name := x.Name
		switch name {
		case "string":
			return "string"
		case "bool":
			return "boolean"
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64", "byte", "rune":
			return "number"
		case "any", "interface{}":
			return "unknown"
		}
		for _, typ := range mod.Types {
			if typ.Node == nil || typ.Node.Name == nil {
				continue
			}
			if name != typ.Name && name != typ.Node.Name.Name {
				continue
			}
			key := typ.Node.Name.Name
			if visiting[key] {
				return "unknown"
			}
			visiting[key] = true
			out := clientJSONExprTSType(mod, typ.Node.Type, visiting)
			delete(visiting, key)
			return out
		}
		return "unknown"
	case *ast.StarExpr:
		return clientJSONExprTSType(mod, x.X, visiting) + " | null"
	case *ast.ArrayType:
		if id, ok := x.Elt.(*ast.Ident); ok && id.Name == "byte" {
			return "string"
		}
		return "Array<" + clientJSONExprTSType(mod, x.Elt, visiting) + ">"
	case *ast.MapType:
		key := clientJSONExprTSType(mod, x.Key, visiting)
		if key != "string" && key != "number" {
			return "unknown"
		}
		return "Record<" + key + ", " + clientJSONExprTSType(mod, x.Value, visiting) + ">"
	case *ast.InterfaceType:
		return "unknown"
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" && x.Sel.Name == "Time" {
			return "string"
		}
		return "unknown"
	case *ast.StructType:
		fields := make([]string, 0)
		for _, field := range x.Fields.List {
			if len(field.Names) == 0 {
				continue
			}
			fieldType := clientJSONExprTSType(mod, field.Type, visiting)
			for _, fieldName := range field.Names {
				name, omit := actionJSONFieldName(fieldName.Name, field.Tag)
				if omit || name == "" || !fieldName.IsExported() {
					continue
				}
				fields = append(fields, fmt.Sprintf("%s: %s", tsPropertyName(name), fieldType))
			}
		}
		if len(fields) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(fields, "; ") + " }"
	}
	return "unknown"
}

func actionJSONFieldName(goName string, tag *ast.BasicLit) (string, bool) {
	if tag == nil {
		return goName, false
	}
	raw, err := strconv.Unquote(tag.Value)
	if err != nil {
		return goName, false
	}
	jsonTag := reflect.StructTag(raw).Get("json")
	if jsonTag == "" {
		return goName, false
	}
	name := strings.Split(jsonTag, ",")[0]
	if name == "-" {
		return "", true
	}
	if name == "" {
		return goName, false
	}
	return name, false
}

func tsPropertyName(name string) string {
	if token.IsIdentifier(name) {
		return name
	}
	return strconv.Quote(name)
}

func renderActionTypes(reg actionTypeRegistry) []byte {
	all := []actionTypeDecl{}
	for _, decls := range reg.Sources {
		all = append(all, decls...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		if all[i].Source != all[j].Source {
			return all[i].Source < all[j].Source
		}
		return len(all[i].Params) < len(all[j].Params)
	})
	var b strings.Builder
	b.WriteString("// Code generated by GOX. DO NOT EDIT.\n\ndeclare module \"@gox/actions\" {\n  export class GoxActionError extends Error {\n    readonly code: number\n    readonly type: string\n    readonly action: string\n    readonly status: number\n    readonly cause?: unknown\n  }\n\n  export interface ServerActions {\n")
	seen := map[string]bool{}
	for _, d := range all {
		parts := make([]string, len(d.Params))
		for i, p := range d.Params {
			name := p.Name
			if name == "" {
				name = fmt.Sprintf("arg%d", i+1)
			}
			parts[i] = name + ": " + p.Type
		}
		result := strings.Join(d.Results, " | ")
		if result == "" {
			result = "null"
		}
		key := d.Name + "(" + strings.Join(parts, ",") + "):" + result + "@" + d.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(&b, "    /** Defined in `%s` */\n    %s(%s): Promise<[%s | null, GoxActionError | null]>\n", d.Source, d.Name, strings.Join(parts, ", "), result)
	}
	b.WriteString("  }\n\n  const Actions: ServerActions\n  export default Actions\n}\n")
	return []byte(b.String())
}

func writeAtomicIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
