package gox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

var goxLineMarkerRE = regexp.MustCompile(`^__goxLine\(("(?:\\.|[^"])*"), ([0-9]+), ([0-9]+)\)$`)

func WriteBundle(bundle *Bundle, outPath string) error {
	src, sourceMap, err := RenderBundleMapped(bundle, outPath)
	if err != nil {
		return err
	}
	data, err := goxmap.MarshalJSONDocument(sourceMap, src, bundle.Sources)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(outPath, []byte(src), 0644); err != nil {
		return err
	}
	return os.WriteFile(BundleSourceMapPath(outPath), data, 0644)
}

func BundleSourceMapPath(outPath string) string {
	return outPath + ".sourcemap.json"
}

// RewriteBundleSourceMapGeneratedURI keeps a persisted source map aligned when
// the generated Go artifact is moved to its final build-version path.
func RewriteBundleSourceMapGeneratedURI(sourceMapPath, generatedURI string) error {
	data, err := os.ReadFile(sourceMapPath)
	if err != nil {
		return err
	}
	var document goxmap.JSONDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	document.GeneratedURI = generatedURI
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(sourceMapPath, data, 0644)
}

func RenderBundleMapped(bundle *Bundle, outPath string) (string, *goxmap.Map, error) {
	formattedURI := outPath + "#formatted"
	formatted, formattedMap, err := FormatBundleMapped(bundle, formattedURI)
	if err != nil {
		return "", nil, err
	}
	finalSource, rewriteMap, err := rewriteGoxLineMarkersMapped(formatted, formattedURI, outPath, bundle.GoImports)
	if err != nil {
		return "", nil, err
	}
	finalMap, err := goxmap.Compose(outPath, rewriteMap, formattedMap)
	if err != nil {
		return "", nil, err
	}
	return finalSource, finalMap, nil
}

func rewriteGoxLineMarkers(src string, generatedPath string, imports []ImportDecl) string {
	projectRoot := inferProjectRootFromGeneratedPath(generatedPath)
	importLines := buildImportLineDirectiveIndex(imports, projectRoot)

	var out strings.Builder
	out.Grow(len(src) + len(importLines)*48)

	lines := strings.SplitAfter(src, "\n")
	for i, line := range lines {
		hasNewline := strings.HasSuffix(line, "\n")
		lineBody := strings.TrimSuffix(line, "\n")
		trimmed := strings.TrimSpace(lineBody)

		if m := goxLineMarkerRE.FindStringSubmatch(trimmed); m != nil {
			file, err := strconv.Unquote(m[1])
			if err == nil && file != "" {
				writeLineDirective(&out, goxLineUserPath(file, projectRoot), m[2], "1", hasNewline)
				continue
			}
		}

		if trimmed == "__goxLineReset()" {
			writeLineDirective(&out, filepath.ToSlash(generatedPath), strconv.Itoa(i+2), "1", hasNewline)
			continue
		}

		if directive, ok := importLines[trimmed]; ok && directive != "" {
			out.WriteString(directive)
			out.WriteByte('\n')
		}

		out.WriteString(line)
	}

	return out.String()
}

func rewriteGoxLineMarkersMapped(src string, sourceURI string, generatedPath string, imports []ImportDecl) (string, *goxmap.Map, error) {
	writer, err := goxmap.NewWriter(generatedPath, sourceURI, src)
	if err != nil {
		return "", nil, err
	}
	projectRoot := inferProjectRootFromGeneratedPath(generatedPath)
	importLines := buildImportLineDirectiveIndex(imports, projectRoot)
	lines := strings.SplitAfter(src, "\n")
	offset := 0
	for index, line := range lines {
		start := offset
		end := start + len(line)
		offset = end
		hasNewline := strings.HasSuffix(line, "\n")
		bodyEnd := end
		if hasNewline {
			bodyEnd--
		}
		lineBody := strings.TrimSuffix(line, "\n")
		trimmed := strings.TrimSpace(lineBody)
		anchor := goxmap.Range{Start: start, End: bodyEnd}

		if match := goxLineMarkerRE.FindStringSubmatch(trimmed); match != nil {
			file, unquoteErr := strconv.Unquote(match[1])
			if unquoteErr == nil && file != "" {
				directive := lineDirectiveText(goxLineUserPath(file, projectRoot), match[2], "1", hasNewline)
				if err := writer.WriteGenerated(directive, anchor); err != nil {
					return "", nil, err
				}
				continue
			}
		}

		if trimmed == "__goxLineReset()" {
			directive := lineDirectiveText(filepath.ToSlash(generatedPath), strconv.Itoa(index+2), "1", hasNewline)
			if err := writer.WriteGenerated(directive, anchor); err != nil {
				return "", nil, err
			}
			continue
		}

		if directive, ok := importLines[trimmed]; ok && directive != "" {
			if err := writer.WriteGenerated(directive+"\n", anchor); err != nil {
				return "", nil, err
			}
		}
		if err := writer.WriteSource(goxmap.Range{Start: start, End: end}); err != nil {
			return "", nil, err
		}
	}
	return writer.Build()
}

func lineDirectiveText(file string, line string, column string, newline bool) string {
	text := fmt.Sprintf("//line %s:%s:%s", file, line, column)
	if newline {
		text += "\n"
	}
	return text
}

func buildImportLineDirectiveIndex(imports []ImportDecl, projectRoot string) map[string]string {
	if len(imports) == 0 {
		return nil
	}

	out := make(map[string]string, len(imports))

	for _, imp := range imports {
		if imp.SourceFile == "" || imp.SourceLine <= 0 {
			continue
		}

		key := importLineKey(imp)
		if key == "" {
			continue
		}

		file := goxLineUserPath(imp.SourceFile, projectRoot)
		out[key] = "//line " + file + ":" + strconv.Itoa(imp.SourceLine) + ":1"
	}

	return out
}

func importLineKey(imp ImportDecl) string {
	if imp.Path == "" {
		return ""
	}

	quoted := strconv.Quote(imp.Path)
	if imp.Alias == "" {
		return quoted
	}

	return imp.Alias + " " + quoted
}

func inferProjectRootFromGeneratedPath(generatedPath string) string {
	if generatedPath == "" {
		return ""
	}

	clean := filepath.Clean(generatedPath)
	parts := strings.Split(filepath.ToSlash(clean), "/.build/")
	if len(parts) >= 2 {
		return filepath.FromSlash(parts[0])
	}

	return ""
}

func goxLineUserPath(file string, projectRoot string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return file
	}

	clean := filepath.Clean(file)
	if projectRoot != "" {
		if rel, err := filepath.Rel(projectRoot, clean); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}

	return normalizeGoxUserPath(file)
}

func normalizeGoxUserPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}

	slash := filepath.ToSlash(path)
	if strings.HasPrefix(slash, "./") {
		slash = strings.TrimPrefix(slash, "./")
	}

	if idx := strings.Index(slash, "/pages/"); idx >= 0 {
		return slash[idx+1:]
	}
	if strings.HasPrefix(slash, "pages/") {
		return slash
	}

	return slash
}

func writeLineDirective(out *strings.Builder, file string, line string, col string, newline bool) {
	out.WriteString("//line ")
	out.WriteString(file)
	out.WriteString(":")
	out.WriteString(line)
	out.WriteString(":")
	out.WriteString(col)
	if newline {
		out.WriteByte('\n')
	}
}
