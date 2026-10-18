package gox

import (
	"go/ast"
	"go/parser"
	"go/token"
)

var goxRuntimeFunctions = map[string]struct{}{
	"GoxWriteTextString":           {},
	"GoxWriteRawString":            {},
	"GoxWriteTextBytes":            {},
	"GoxWriteRawBytes":             {},
	"GoxWriteTextBool":             {},
	"GoxWriteRawBool":              {},
	"GoxWriteTextInt":              {},
	"GoxWriteRawInt":               {},
	"GoxWriteTextInt8":             {},
	"GoxWriteRawInt8":              {},
	"GoxWriteTextInt16":            {},
	"GoxWriteRawInt16":             {},
	"GoxWriteTextInt32":            {},
	"GoxWriteRawInt32":             {},
	"GoxWriteTextInt64":            {},
	"GoxWriteRawInt64":             {},
	"GoxWriteTextUint":             {},
	"GoxWriteRawUint":              {},
	"GoxWriteTextUint8":            {},
	"GoxWriteRawUint8":             {},
	"GoxWriteTextUint16":           {},
	"GoxWriteRawUint16":            {},
	"GoxWriteTextUint32":           {},
	"GoxWriteRawUint32":            {},
	"GoxWriteTextUint64":           {},
	"GoxWriteRawUint64":            {},
	"GoxWriteTextUintptr":          {},
	"GoxWriteRawUintptr":           {},
	"GoxWriteTextFloat32":          {},
	"GoxWriteRawFloat32":           {},
	"GoxWriteTextFloat64":          {},
	"GoxWriteRawFloat64":           {},
	"GoxWriteTextBigInt":           {},
	"GoxWriteRawBigInt":            {},
	"GoxWriteTextBigIntValue":      {},
	"GoxWriteRawBigIntValue":       {},
	"GoxWriteTextBigFloat":         {},
	"GoxWriteRawBigFloat":          {},
	"GoxWriteTextBigFloatValue":    {},
	"GoxWriteRawBigFloatValue":     {},
	"GoxWriteTextBigRat":           {},
	"GoxWriteRawBigRat":            {},
	"GoxWriteTextBigRatValue":      {},
	"GoxWriteRawBigRatValue":       {},
	"GoxWriteTextAny":              {},
	"GoxWriteRawAny":               {},
	"GoxWriteSpreadAttrs":          {},
	"GoxInvalidActionPayloadError": {},
}

func DetectUsedRuntimeHelpers(decls []ast.Decl) map[string]struct{} {
	used := map[string]struct{}{}

	ast.Inspect(&ast.File{Decls: decls}, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}

		if _, exists := goxRuntimeFunctions[id.Name]; exists {
			used[id.Name] = struct{}{}
		}

		return true
	})

	return expandRuntimeDependencies(used)
}

func expandRuntimeDependencies(used map[string]struct{}) map[string]struct{} {
	deps := map[string][]string{
		"GoxWriteTextString": {"goxWriteEscapedString", "goxEscAmp", "goxEscLT", "goxEscGT", "goxEscQuote", "goxEscApos"},
		"GoxWriteRawString":  {"goxWriteRawStringBytes"},
		"GoxWriteTextBytes":  {"goxWriteEscapedBytes", "goxEscAmp", "goxEscLT", "goxEscGT", "goxEscQuote", "goxEscApos"},
		"GoxWriteTextBool":   {"goxBoolTrue", "goxBoolFalse"},
		"GoxWriteRawBool":    {"GoxWriteTextBool"},

		"GoxWriteTextInt":     {},
		"GoxWriteTextInt8":    {},
		"GoxWriteTextInt16":   {},
		"GoxWriteTextInt32":   {},
		"GoxWriteTextInt64":   {},
		"GoxWriteTextUint":    {},
		"GoxWriteTextUint8":   {},
		"GoxWriteTextUint16":  {},
		"GoxWriteTextUint32":  {},
		"GoxWriteTextUint64":  {},
		"GoxWriteTextUintptr": {},
		"GoxWriteTextFloat32": {},
		"GoxWriteTextFloat64": {},

		"GoxWriteRawInt":     {"GoxWriteTextInt"},
		"GoxWriteRawInt8":    {"GoxWriteTextInt8"},
		"GoxWriteRawInt16":   {"GoxWriteTextInt16"},
		"GoxWriteRawInt32":   {"GoxWriteTextInt32"},
		"GoxWriteRawInt64":   {"GoxWriteTextInt64"},
		"GoxWriteRawUint":    {"GoxWriteTextUint"},
		"GoxWriteRawUint8":   {"GoxWriteTextUint8"},
		"GoxWriteRawUint16":  {"GoxWriteTextUint16"},
		"GoxWriteRawUint32":  {"GoxWriteTextUint32"},
		"GoxWriteRawUint64":  {"GoxWriteTextUint64"},
		"GoxWriteRawUintptr": {"GoxWriteTextUintptr"},
		"GoxWriteRawFloat32": {"GoxWriteTextFloat32"},
		"GoxWriteRawFloat64": {"GoxWriteTextFloat64"},

		"GoxWriteTextBigInt":        {"goxWriteBigString"},
		"GoxWriteRawBigInt":         {"GoxWriteTextBigInt"},
		"GoxWriteTextBigIntValue":   {"goxWriteBigString"},
		"GoxWriteRawBigIntValue":    {"GoxWriteTextBigIntValue"},
		"GoxWriteTextBigFloat":      {"goxWriteBigString"},
		"GoxWriteRawBigFloat":       {"GoxWriteTextBigFloat"},
		"GoxWriteTextBigFloatValue": {"goxWriteBigString"},
		"GoxWriteRawBigFloatValue":  {"GoxWriteTextBigFloatValue"},
		"GoxWriteTextBigRat":        {"goxWriteBigString"},
		"GoxWriteRawBigRat":         {"GoxWriteTextBigRat"},
		"GoxWriteTextBigRatValue":   {"goxWriteBigString"},
		"GoxWriteRawBigRatValue":    {"GoxWriteTextBigRatValue"},
		"goxWriteEscapedString":     {"goxEscAmp", "goxEscLT", "goxEscGT", "goxEscQuote", "goxEscApos"},
		"goxWriteEscapedBytes":      {"goxEscAmp", "goxEscLT", "goxEscGT", "goxEscQuote", "goxEscApos"},
		"goxWriteRawStringBytes":    {},
		"goxWriteBigString":         {"GoxWriteRawString"},

		"GoxWriteTextAny": {"goxWriteAny"},
		"GoxWriteRawAny":  {"goxWriteAny"},
		"goxWriteAny": {
			"GoxWriteTextString", "GoxWriteRawString", "GoxWriteTextBytes", "GoxWriteRawBytes", "GoxWriteTextBool",
			"GoxWriteTextInt", "GoxWriteTextInt8", "GoxWriteTextInt16", "GoxWriteTextInt32", "GoxWriteTextInt64",
			"GoxWriteTextUint", "GoxWriteTextUint8", "GoxWriteTextUint16", "GoxWriteTextUint32", "GoxWriteTextUint64", "GoxWriteTextUintptr",
			"GoxWriteTextFloat32", "GoxWriteTextFloat64",
			"GoxWriteTextBigInt", "GoxWriteTextBigIntValue", "GoxWriteTextBigFloat", "GoxWriteTextBigFloatValue", "GoxWriteTextBigRat", "GoxWriteTextBigRatValue",
		},

		"GoxWriteSpreadAttrs": {"GoxWriteTextString", "goxWriteRawStringBytes", "goxAttrSpace", "goxAttrEqOpen", "goxAttrQuote"},
	}

	changed := true
	for changed {
		changed = false
		for name := range used {
			for _, dep := range deps[name] {
				if _, ok := used[dep]; ok {
					continue
				}
				used[dep] = struct{}{}
				changed = true
			}
		}
	}

	return used
}

func BuildRuntimeDeclsFromFile(runtimePath string, used map[string]struct{}) ([]ast.Decl, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, runtimePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return filterRuntimeDecls(file, used), nil
}

func BuildRuntimeDecls(used map[string]struct{}) ([]ast.Decl, error) {
	return buildRuntimeDeclsFromSource(goxRuntimeSource, used)
}

func buildRuntimeDeclsFromSource(source string, used map[string]struct{}) ([]ast.Decl, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "gox_runtime.go", source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return filterRuntimeDecls(file, used), nil
}

func filterRuntimeDecls(file *ast.File, used map[string]struct{}) []ast.Decl {
	var decls []ast.Decl

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if _, exists := used[d.Name.Name]; exists {
				decls = append(decls, d)
			}

		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				var specs []ast.Spec
				for _, spec := range d.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, exists := used[typeSpec.Name.Name]; exists {
						specs = append(specs, typeSpec)
					}
				}
				if len(specs) == 0 {
					continue
				}
				clone := *d
				clone.Specs = specs
				decls = append(decls, &clone)
				continue
			}

			if d.Tok != token.VAR && d.Tok != token.CONST {
				continue
			}

			var specs []ast.Spec
			for _, spec := range d.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				keepNames := []*ast.Ident{}
				keepValues := []ast.Expr{}
				for i, name := range valueSpec.Names {
					if _, exists := used[name.Name]; !exists {
						continue
					}
					keepNames = append(keepNames, name)
					if i < len(valueSpec.Values) {
						keepValues = append(keepValues, valueSpec.Values[i])
					}
				}

				if len(keepNames) == 0 {
					continue
				}

				clone := *valueSpec
				clone.Names = keepNames
				clone.Values = keepValues
				specs = append(specs, &clone)
			}

			if len(specs) == 0 {
				continue
			}

			clone := *d
			clone.Specs = specs
			decls = append(decls, &clone)
		}
	}

	return decls
}

const goxRuntimeSource = `package main

import (
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
)

var (
	goxEscAmp   = []byte("&amp;")
	goxEscLT    = []byte("&lt;")
	goxEscGT    = []byte("&gt;")
	goxEscQuote = []byte("&#34;")
	goxEscApos  = []byte("&#39;")

	goxBoolTrue  = []byte("true")
	goxBoolFalse = []byte("false")

	goxAttrSpace  = []byte(" ")
	goxAttrEqOpen = []byte("=\"")
	goxAttrQuote  = []byte("\"")
)

func GoxWriteTextString(w io.Writer, value string) {
	goxWriteEscapedString(w, value)
}

func GoxWriteRawString(w io.Writer, value string) {
	goxWriteRawStringBytes(w, value)
}

func GoxWriteTextBytes(w io.Writer, value []byte) {
	goxWriteEscapedBytes(w, value)
}

func GoxWriteRawBytes(w io.Writer, value []byte) {
	if len(value) == 0 {
		return
	}
	_, _ = w.Write(value)
}

func GoxWriteTextBool(w io.Writer, value bool) {
	if value {
		_, _ = w.Write(goxBoolTrue)
		return
	}
	_, _ = w.Write(goxBoolFalse)
}

func GoxWriteRawBool(w io.Writer, value bool) { GoxWriteTextBool(w, value) }

func GoxWriteTextInt(w io.Writer, value int) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendInt(buf[:0], int64(value), 10))
}
func GoxWriteRawInt(w io.Writer, value int) { GoxWriteTextInt(w, value) }

func GoxWriteTextInt8(w io.Writer, value int8) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendInt(buf[:0], int64(value), 10))
}
func GoxWriteRawInt8(w io.Writer, value int8) { GoxWriteTextInt8(w, value) }

func GoxWriteTextInt16(w io.Writer, value int16) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendInt(buf[:0], int64(value), 10))
}
func GoxWriteRawInt16(w io.Writer, value int16) { GoxWriteTextInt16(w, value) }

func GoxWriteTextInt32(w io.Writer, value int32) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendInt(buf[:0], int64(value), 10))
}
func GoxWriteRawInt32(w io.Writer, value int32) { GoxWriteTextInt32(w, value) }

func GoxWriteTextInt64(w io.Writer, value int64) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendInt(buf[:0], value, 10))
}
func GoxWriteRawInt64(w io.Writer, value int64) { GoxWriteTextInt64(w, value) }

func GoxWriteTextUint(w io.Writer, value uint) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendUint(buf[:0], uint64(value), 10))
}
func GoxWriteRawUint(w io.Writer, value uint) { GoxWriteTextUint(w, value) }

func GoxWriteTextUint8(w io.Writer, value uint8) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendUint(buf[:0], uint64(value), 10))
}
func GoxWriteRawUint8(w io.Writer, value uint8) { GoxWriteTextUint8(w, value) }

func GoxWriteTextUint16(w io.Writer, value uint16) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendUint(buf[:0], uint64(value), 10))
}
func GoxWriteRawUint16(w io.Writer, value uint16) { GoxWriteTextUint16(w, value) }

func GoxWriteTextUint32(w io.Writer, value uint32) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendUint(buf[:0], uint64(value), 10))
}
func GoxWriteRawUint32(w io.Writer, value uint32) { GoxWriteTextUint32(w, value) }

func GoxWriteTextUint64(w io.Writer, value uint64) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendUint(buf[:0], value, 10))
}
func GoxWriteRawUint64(w io.Writer, value uint64) { GoxWriteTextUint64(w, value) }

func GoxWriteTextUintptr(w io.Writer, value uintptr) {
	var buf [32]byte
	_, _ = w.Write(strconv.AppendUint(buf[:0], uint64(value), 10))
}
func GoxWriteRawUintptr(w io.Writer, value uintptr) { GoxWriteTextUintptr(w, value) }

func GoxWriteTextFloat32(w io.Writer, value float32) {
	var buf [64]byte
	_, _ = w.Write(strconv.AppendFloat(buf[:0], float64(value), 'f', -1, 32))
}
func GoxWriteRawFloat32(w io.Writer, value float32) { GoxWriteTextFloat32(w, value) }

func GoxWriteTextFloat64(w io.Writer, value float64) {
	var buf [64]byte
	_, _ = w.Write(strconv.AppendFloat(buf[:0], value, 'f', -1, 64))
}
func GoxWriteRawFloat64(w io.Writer, value float64) { GoxWriteTextFloat64(w, value) }

func GoxWriteTextBigInt(w io.Writer, value *big.Int) {
	if value == nil {
		return
	}
	goxWriteBigString(w, value.String())
}
func GoxWriteRawBigInt(w io.Writer, value *big.Int) { GoxWriteTextBigInt(w, value) }

func GoxWriteTextBigIntValue(w io.Writer, value big.Int) { goxWriteBigString(w, value.String()) }
func GoxWriteRawBigIntValue(w io.Writer, value big.Int)  { GoxWriteTextBigIntValue(w, value) }

func GoxWriteTextBigFloat(w io.Writer, value *big.Float) {
	if value == nil {
		return
	}
	goxWriteBigString(w, value.Text('g', -1))
}
func GoxWriteRawBigFloat(w io.Writer, value *big.Float) { GoxWriteTextBigFloat(w, value) }

func GoxWriteTextBigFloatValue(w io.Writer, value big.Float) {
	goxWriteBigString(w, value.Text('g', -1))
}
func GoxWriteRawBigFloatValue(w io.Writer, value big.Float) { GoxWriteTextBigFloatValue(w, value) }

func GoxWriteTextBigRat(w io.Writer, value *big.Rat) {
	if value == nil {
		return
	}
	goxWriteBigString(w, value.RatString())
}
func GoxWriteRawBigRat(w io.Writer, value *big.Rat) { GoxWriteTextBigRat(w, value) }

func GoxWriteTextBigRatValue(w io.Writer, value big.Rat) { goxWriteBigString(w, value.RatString()) }
func GoxWriteRawBigRatValue(w io.Writer, value big.Rat)  { GoxWriteTextBigRatValue(w, value) }

func GoxWriteTextAny(w io.Writer, value any) { goxWriteAny(w, value, true) }
func GoxWriteRawAny(w io.Writer, value any)  { goxWriteAny(w, value, false) }

func goxWriteAny(w io.Writer, value any, escape bool) {
	switch v := value.(type) {
	case string:
		if escape { GoxWriteTextString(w, v) } else { GoxWriteRawString(w, v) }
	case []byte:
		if escape { GoxWriteTextBytes(w, v) } else { GoxWriteRawBytes(w, v) }
	case bool:
		GoxWriteTextBool(w, v)
	case int:
		GoxWriteTextInt(w, v)
	case int8:
		GoxWriteTextInt8(w, v)
	case int16:
		GoxWriteTextInt16(w, v)
	case int32:
		GoxWriteTextInt32(w, v)
	case int64:
		GoxWriteTextInt64(w, v)
	case uint:
		GoxWriteTextUint(w, v)
	case uint8:
		GoxWriteTextUint8(w, v)
	case uint16:
		GoxWriteTextUint16(w, v)
	case uint32:
		GoxWriteTextUint32(w, v)
	case uint64:
		GoxWriteTextUint64(w, v)
	case uintptr:
		GoxWriteTextUintptr(w, v)
	case float32:
		GoxWriteTextFloat32(w, v)
	case float64:
		GoxWriteTextFloat64(w, v)
	case *big.Int:
		GoxWriteTextBigInt(w, v)
	case big.Int:
		GoxWriteTextBigIntValue(w, v)
	case *big.Float:
		GoxWriteTextBigFloat(w, v)
	case big.Float:
		GoxWriteTextBigFloatValue(w, v)
	case *big.Rat:
		GoxWriteTextBigRat(w, v)
	case big.Rat:
		GoxWriteTextBigRatValue(w, v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			if escape { GoxWriteTextString(w, "[Object]") } else { GoxWriteRawString(w, "[Object]") }
			return
		}
		if escape { GoxWriteTextBytes(w, b) } else { GoxWriteRawBytes(w, b) }
	}
}

func goxWriteEscapedString(w io.Writer, value string) {
	if value == "" {
		return
	}

	var buf [256]byte
	out := buf[:0]

	for i := 0; i < len(value); i++ {
		var esc []byte

		switch value[i] {
		case '&':
			esc = goxEscAmp
		case '<':
			esc = goxEscLT
		case '>':
			esc = goxEscGT
		case '"':
			esc = goxEscQuote
		case '\'':
			esc = goxEscApos
		default:
			if len(out) == cap(out) {
				_, _ = w.Write(out)
				out = buf[:0]
			}
			out = append(out, value[i])
			continue
		}

		if len(out)+len(esc) > cap(out) {
			if len(out) > 0 {
				_, _ = w.Write(out)
			}
			out = buf[:0]
		}
		out = append(out, esc...)
	}

	if len(out) > 0 {
		_, _ = w.Write(out)
	}
}

func goxWriteEscapedBytes(w io.Writer, value []byte) {
	if len(value) == 0 {
		return
	}

	var buf [256]byte
	out := buf[:0]

	for _, b := range value {
		var esc []byte

		switch b {
		case '&':
			esc = goxEscAmp
		case '<':
			esc = goxEscLT
		case '>':
			esc = goxEscGT
		case '"':
			esc = goxEscQuote
		case '\'':
			esc = goxEscApos
		default:
			if len(out) == cap(out) {
				_, _ = w.Write(out)
				out = buf[:0]
			}
			out = append(out, b)
			continue
		}

		if len(out)+len(esc) > cap(out) {
			if len(out) > 0 {
				_, _ = w.Write(out)
			}
			out = buf[:0]
		}
		out = append(out, esc...)
	}

	if len(out) > 0 {
		_, _ = w.Write(out)
	}
}

func goxWriteRawStringBytes(w io.Writer, value string) {
	if value == "" {
		return
	}

	var buf [256]byte
	out := buf[:0]

	for i := 0; i < len(value); i++ {
		if len(out) == cap(out) {
			_, _ = w.Write(out)
			out = buf[:0]
		}
		out = append(out, value[i])
	}

	if len(out) > 0 {
		_, _ = w.Write(out)
	}
}

func goxWriteBigString(w io.Writer, value string) {
	GoxWriteRawString(w, value)
}

func GoxWriteSpreadAttrs(w io.Writer, attrs map[string]string) {
	for k, v := range attrs {
		_, _ = w.Write(goxAttrSpace)
		GoxWriteTextString(w, k)
		_, _ = w.Write(goxAttrEqOpen)
		GoxWriteTextString(w, v)
		_, _ = w.Write(goxAttrQuote)
	}
}

func GoxInvalidActionPayloadError() error {
	return errors.New("invalid action payload")
}
`

func RuntimeImportsForUsedHelpers(used map[string]struct{}) []ImportDecl {
	imports := []ImportDecl{}
	add := func(path string) {
		imports = appendImportIfMissing(imports, ImportDecl{Path: path})
	}

	for name := range used {
		switch name {
		case "GoxWriteTextAny", "GoxWriteRawAny", "goxWriteAny":
			add("encoding/json")
		}
		if runtimeUsesIO(name) {
			add("io")
		}
		if runtimeUsesStrconv(name) {
			add("strconv")
		}
		if runtimeUsesBig(name) {
			add("math/big")
		}
		if name == "GoxInvalidActionPayloadError" {
			add("errors")
		}
	}

	return imports
}

func runtimeUsesIO(name string) bool {
	switch name {
	case "GoxInvalidActionPayloadError":
		return false
	}
	_, isFn := goxRuntimeFunctions[name]
	if isFn {
		return true
	}
	switch name {
	case "goxWriteAny", "goxWriteEscapedString", "goxWriteEscapedBytes", "goxWriteRawStringBytes", "goxWriteBigString":
		return true
	}
	return false
}

func runtimeUsesStrconv(name string) bool {
	switch name {
	case "GoxWriteTextInt", "GoxWriteTextInt8", "GoxWriteTextInt16", "GoxWriteTextInt32", "GoxWriteTextInt64",
		"GoxWriteTextUint", "GoxWriteTextUint8", "GoxWriteTextUint16", "GoxWriteTextUint32", "GoxWriteTextUint64", "GoxWriteTextUintptr",
		"GoxWriteTextFloat32", "GoxWriteTextFloat64":
		return true
	}
	return false
}

func runtimeUsesBig(name string) bool {
	switch name {
	case "GoxWriteTextBigInt", "GoxWriteRawBigInt", "GoxWriteTextBigIntValue", "GoxWriteRawBigIntValue",
		"GoxWriteTextBigFloat", "GoxWriteRawBigFloat", "GoxWriteTextBigFloatValue", "GoxWriteRawBigFloatValue",
		"GoxWriteTextBigRat", "GoxWriteRawBigRat", "GoxWriteTextBigRatValue", "GoxWriteRawBigRatValue", "goxWriteAny":
		return true
	}
	return false
}
