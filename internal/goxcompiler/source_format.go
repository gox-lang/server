package gox

import (
	"fmt"
	"go/format"
	"go/token"
	"sort"
	"strings"
)

type formatProtectedRange struct {
	start       int
	end         int
	placeholder string
	raw         string
	template    bool
}

// FormatSource formats the Go portion and template values of a GOX document
// while preserving client declarations byte-for-byte.
func FormatSource(path, source string) (string, error) {
	moduleStart, moduleEnd, ok := moduleDeclarationRange(source)
	if !ok {
		return "", fmt.Errorf("module declaration not found")
	}
	moduleName := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source[moduleStart:moduleEnd]), "module "))
	if !isValidIdent(moduleName) {
		return "", fmt.Errorf("invalid module name %q", moduleName)
	}
	extracted, err := ExtractTemplateReturns(moduleName, path, source)
	if err != nil {
		return "", err
	}

	protected, err := formatProtectedRanges(path, source, extracted.Templates)
	if err != nil {
		return "", err
	}
	transformed := source[:moduleStart] + "package " + moduleName + source[moduleEnd:]
	delta := len("package "+moduleName) - (moduleEnd - moduleStart)
	for index := range protected {
		if protected[index].start >= moduleEnd {
			protected[index].start += delta
			protected[index].end += delta
		}
	}

	for index := len(protected) - 1; index >= 0; index-- {
		item := protected[index]
		transformed = transformed[:item.start] + item.placeholder + transformed[item.end:]
	}
	formatted, err := format.Source([]byte(transformed))
	if err != nil {
		return "", err
	}
	result := string(formatted)
	packageLine := "package " + moduleName
	moduleLine := "module " + moduleName
	if position := formattedPackageOffset(result, packageLine); position >= 0 {
		result = result[:position] + moduleLine + result[position+len(packageLine):]
	}
	for _, item := range protected {
		if count := strings.Count(result, item.placeholder); count != 1 {
			return "", fmt.Errorf("formatter placeholder %q occurred %d times", item.placeholder, count)
		}
		replacement := item.raw
		if item.template {
			position := strings.Index(result, item.placeholder)
			lineStart := strings.LastIndexByte(result[:position], '\n') + 1
			linePrefix := result[lineStart:position]
			indent := linePrefix[:len(linePrefix)-len(strings.TrimLeft(linePrefix, " \t"))]
			replacement, err = FormatTemplate(item.raw, indent)
			if err != nil {
				return "", err
			}
		}
		result = strings.Replace(result, item.placeholder, replacement, 1)
	}
	return result, nil
}

func formattedPackageOffset(source, declaration string) int {
	for start := 0; start <= len(source); {
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start
		}
		if strings.TrimSpace(source[start:end]) == declaration {
			return start + len(source[start:end]) - len(strings.TrimLeft(source[start:end], " \t"))
		}
		if end == len(source) {
			break
		}
		start = end + 1
	}
	return -1
}

func formatProtectedRanges(path, source string, returns []TemplateReturn) ([]formatProtectedRange, error) {
	var ranges []formatProtectedRange
	for _, item := range returns {
		start := item.Start
		if start >= 2 && source[start-2:start] == "<>" {
			start -= 2
		}
		ranges = append(ranges, formatProtectedRange{start: start, end: item.End, raw: source[start:item.End], template: true})
	}
	clients, err := formatClientRanges(path, source)
	if err != nil {
		return nil, err
	}
	ranges = append(ranges, clients...)

	for offset := 0; offset < len(source); {
		if formatRangeContains(ranges, offset) {
			offset = formatContainingRangeEnd(ranges, offset)
			continue
		}
		if shouldSkipGoStringOrComment(source, offset) {
			next, skipErr := skipGoStringOrComment(source, offset)
			if skipErr == nil && next > offset {
				offset = next
				continue
			}
		}
		if source[offset] != '<' || !isTemplateValuePositionStart(source, offset) {
			offset++
			continue
		}
		var end int
		var readErr error
		switch {
		case looksLikeTemplateFragmentStart(source, offset):
			_, end, _, readErr = readTemplateFragmentByDelim(path, source, offset)
		case looksLikeTemplateExpressionStart(source, offset):
			_, end, readErr = readTemplateExpressionByHTML(path, source, offset)
		default:
			offset++
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		ranges = append(ranges, formatProtectedRange{start: offset, end: end, raw: source[offset:end], template: true})
		offset = end
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for index := range ranges {
		if index > 0 && ranges[index].start < ranges[index-1].end {
			return nil, fmt.Errorf("overlapping GOX formatter ranges")
		}
		if strings.HasPrefix(strings.TrimSpace(ranges[index].raw), "client ") {
			ranges[index].placeholder = fmt.Sprintf("var __gox_format_client_%d = struct{}{}", index+1)
		} else {
			ranges[index].placeholder = fmt.Sprintf("__gox_format_template_%d", index+1)
		}
	}
	return ranges, nil
}

func formatClientRanges(path, source string) ([]formatProtectedRange, error) {
	var ranges []formatProtectedRange
	for offset := 0; offset < len(source); {
		if !looksLikeClientDeclAtLineStart(source, offset) || !looksLikeTopLevelClientDecl(source, offset) {
			offset++
			continue
		}
		_, end, err := parseClientDeclAt(path, source, offset, token.Pos(offset+1))
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, formatProtectedRange{start: offset, end: end, raw: source[offset:end]})
		offset = end
	}
	return ranges, nil
}

func formatRangeContains(ranges []formatProtectedRange, offset int) bool {
	for _, item := range ranges {
		if offset >= item.start && offset < item.end {
			return true
		}
	}
	return false
}

func formatContainingRangeEnd(ranges []formatProtectedRange, offset int) int {
	for _, item := range ranges {
		if offset >= item.start && offset < item.end {
			return item.end
		}
	}
	return offset + 1
}

func moduleDeclarationRange(source string) (int, int, bool) {
	for start := 0; start <= len(source); {
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start
		}
		trimmed := strings.TrimSpace(source[start:end])
		if strings.HasPrefix(trimmed, "module ") {
			leading := len(source[start:end]) - len(strings.TrimLeft(source[start:end], " \t"))
			return start + leading, end, true
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			return 0, 0, false
		}
		if end == len(source) {
			break
		}
		start = end + 1
	}
	return 0, 0, false
}
