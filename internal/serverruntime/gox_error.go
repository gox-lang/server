package serverruntime

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func newGoxError(stage gox.GoxErrorStage, route *GoxRouteRuntime, ctx *gox.Context, message string, cause error) *GoxError {
	if cause != nil {
		var core *GoxError
		if errors.As(cause, &core) {
			applyGoxErrorContext(core, route, ctx)
			if core.Stage == "" {
				core.Stage = stage
			}
			if core.Message == "" {
				core.Message = message
			}
			translateGoxError(core)
			return core
		}
	}

	err := NewGoxError(stage, message, cause)
	applyGoxErrorContext(err, route, ctx)
	translateGoxError(err)
	return err
}

func newGoxDispatchError(route *GoxRouteRuntime, ctx *gox.Context, message string, status int) *GoxError {
	err := NewGoxError(gox.GoxErrorDispatch, message, nil)
	err.StatusCode = status
	err.Status = status
	err.EnsureStatus()
	applyGoxErrorContext(err, route, ctx)
	translateGoxError(err)
	return err
}

func newGoxPanicRuntimeError(route *GoxRouteRuntime, ctx *gox.Context, recovered any) *GoxError {
	err := NewGoxPanicError(recovered)
	applyGoxErrorContext(err, route, ctx)
	translateGoxError(err)
	return err
}

func applyGoxErrorContext(err *GoxError, route *GoxRouteRuntime, ctx *gox.Context) {
	if err == nil {
		return
	}
	if ctx != nil {
		if err.Route == "" {
			err.Route = ctx.Request.Route.Target()
		}
		if err.Method == "" {
			err.Method = ctx.Request.Route.Method()
		}
		err.IsAction = ctx.Request.Route.IsAction()
	}
	if route != nil {
		if err.Route == "" {
			err.Route = route.RoutePath
		}
		err.File = route.RelPath
		err.GeneratedFile = route.GeneratedPath
		err.PluginPath = route.PluginPath
		err.AddDetail("buildVersion", route.BuildVersion)
		err.AddDetail("state", string(route.State))
	}
	applyGoxSourceMapDiagnostic(err, route)
	err.EnsureStatus()
}

func asGoxError(err error, stage gox.GoxErrorStage, route *GoxRouteRuntime, ctx *gox.Context, message string) *GoxError {
	if err == nil {
		return nil
	}
	var goxErr *GoxError
	if errors.As(err, &goxErr) {
		applyGoxErrorContext(goxErr, route, ctx)
		translateGoxError(goxErr)
		return goxErr
	}

	return newGoxError(stage, route, ctx, message, err)
}

func (r *Runtime) renderGoxError(ctx *gox.Context, req *http.Request, trace *requestPipelineTrace, err *GoxError) {
	if ctx == nil || err == nil {
		return
	}
	ctx.Error = err
	if shouldLogGoxRequestError(err) {
		logGoxError("request", err)
	}
	err.EnsureStatus()
	status := err.StatusCode
	// Once headers have been sent, record the error without trying to replace
	// the response or write an error page to a failed connection.
	if res, ok := runtimeResponse(ctx); ok && res != nil && res.WroteHeader() {
		return
	}

	if r.renderErrorPage(ctx, req, trace, err) {
		return
	}

	fallbackStart := trace.nowDeep()
	trace.addDeepField("error.handler", "fallback")
	r.renderPlainStatusFallback(ctx, status)
	trace.addDeep("error.handler.fallback", fallbackStart)
}

type goxDiagnostic struct {
	File      string
	Line      int
	Column    int
	Message   string
	Generated bool
}

var (
	goxDiagLineRE           = regexp.MustCompile(`^((?:\.?/)?(?:[A-Za-z]:)?[^:\n]*?\.gox):([0-9]+)(?::([0-9]+))?:\s*(.+)$`)
	goxGeneratedGoDiagRE    = regexp.MustCompile(`^((?:\.?/)?(?:[A-Za-z]:)?[^:\n]*?\.go):([0-9]+):([0-9]+):\s*(.+)$`)
	goxUndefinedRE          = regexp.MustCompile(`^undefined:\s*([A-Za-z_][A-Za-z0-9_]*)$`)
	goxUnusedImportRE       = regexp.MustCompile(`^"([^"]+)" imported and not used$`)
	goxPackageNotSelectorRE = regexp.MustCompile(`^use of package\s+([A-Za-z_][A-Za-z0-9_]*)\s+not in selector$`)
	goxExpectedClosingTagRE = regexp.MustCompile(`^expected closing tag\s+(</[^>]+>),\s*got\s+(</[^>]+>)$`)
	goxCannotUseAsRE        = regexp.MustCompile(`^cannot use\s+(.+)\s+as\s+(.+)$`)
	goxUndefinedFieldRE     = regexp.MustCompile(`^(?:.+\.)?([A-Za-z_][A-Za-z0-9_]*) undefined \(type .+ has no field or method ([A-Za-z_][A-Za-z0-9_]*)\)$`)
	goxNoNewVariablesRE     = regexp.MustCompile(`^no new variables on left side of :=$`)
)

func translateGoxError(err *GoxError) {
	if err == nil {
		return
	}
	if err.Kind != "" {
		completeKnownGoxErrorTranslation(err)
		ensureGoxErrorMessage(err)
		return
	}

	if err.Cause == nil {
		if err.Stage == gox.GoxErrorPanic && err.Message != "" {
			err.Kind = "panic"
			err.Hint = "Check the handler code that panicked and handle the unexpected value before rendering the response."
		}
		ensureGoxErrorMessage(err)
		return
	}

	var core *GoxError
	if errors.As(err.Cause, &core) {
		mergeNestedGoxError(err, core)
		completeKnownGoxErrorTranslation(err)
		ensureGoxErrorMessage(err)
		return
	}

	diagnostics := extractGoxDiagnostics(err.Cause.Error())
	if len(diagnostics) > 0 {
		diag := diagnostics[0]
		if diag.File != "" {
			if !diag.Generated || err.File == "" {
				err.File = normalizeGoxDiagnosticFile(err, diag.File)
			}
		}
		if err.Line == 0 {
			err.Line = diag.Line
		}
		if err.Column == 0 {
			err.Column = diag.Column
		}
		applyGoxDiagnosticTranslation(err, diag.Message)
	}

	ensureGoxErrorMessage(err)
}

func mergeNestedGoxError(dst *GoxError, src *GoxError) {
	if dst == nil || src == nil || dst == src {
		return
	}
	if dst.Type == "" {
		dst.Type = src.Type
	}
	if dst.Kind == "" {
		dst.Kind = src.Kind
	}
	if dst.File == "" {
		dst.File = src.File
	}
	if dst.Line == 0 {
		dst.Line = src.Line
	}
	if dst.Column == 0 {
		dst.Column = src.Column
	}
	if dst.Message == "" || isGenericGoxWrapperMessage(dst.Message) {
		dst.Message = src.Message
	}
	if dst.Hint == "" {
		dst.Hint = src.Hint
	}
	if dst.Expected == "" {
		dst.Expected = src.Expected
	}
	if dst.Found == "" {
		dst.Found = src.Found
	}
	if dst.OriginalError == nil && src.OriginalError != nil {
		dst.OriginalError = src.OriginalError
	}
	for k, v := range src.Details {
		if dst.Details == nil {
			dst.Details = make(map[string]string)
		}
		if dst.Details[k] == "" {
			dst.Details[k] = v
		}
	}
}

func ensureGoxErrorMessage(err *GoxError) {
	if err == nil {
		return
	}
	if err.Kind == "" {
		err.Kind = fallbackGoxErrorKind(err)
	}
	if strings.TrimSpace(err.Message) != "" && !isGenericGoxWrapperMessage(err.Message) {
		return
	}
	if err.Cause != nil {
		if message := firstGoxErrorCauseLine(err.Cause.Error()); message != "" {
			err.Message = message
			return
		}
	}
	if strings.TrimSpace(err.Message) == "" {
		err.Message = "gox " + string(err.Stage) + " error"
	}
}

func fallbackGoxErrorKind(err *GoxError) string {
	if err == nil {
		return "unknown-error"
	}
	switch err.Stage {
	case gox.GoxErrorCompile:
		return "compile-error"
	case gox.GoxErrorBuild:
		return "build-error"
	case gox.GoxErrorLoad:
		return "load-error"
	case gox.GoxErrorWriter:
		return "writer-error"
	case gox.GoxErrorPanic:
		return "panic"
	case gox.GoxErrorDispatch:
		return "dispatch-error"
	case gox.GoxErrorResolve:
		return "resolve-error"
	default:
		if err.Type != "" {
			return string(err.Type) + "-error"
		}
		return "gox-error"
	}
}

func isGenericGoxWrapperMessage(message string) bool {
	switch strings.TrimSpace(message) {
	case "failed to compile gox route",
		"page build failed",
		"page hotReload rebuild failed",
		"failed to resolve gox route",
		"failed to prepare gox route",
		"failed to attach gox writer",
		"error hotReload rebuild failed":
		return true
	default:
		return false
	}
}

func completeKnownGoxErrorTranslation(err *GoxError) {
	switch err.Kind {
	case "template.parser", "template.lexer", "template.unexpected_eof":
		originalKind := err.Kind
		applyGoxDiagnosticTranslation(err, err.Message)
		if err.Kind == originalKind && err.Hint == "" {
			err.Hint = "Review the Gox template syntax near this line."
		}
	case "empty-expression":
		if err.Message == "" || strings.Contains(err.Message, "empty template expression") {
			err.Message = "empty template expression"
		}
		if err.Hint == "" {
			err.Hint = "Add an expression inside { ... } or remove the empty braces."
		}
	case "template.mismatched_closing_tag", "tag-mismatch":
		err.Kind = "tag-mismatch"
		if err.Hint == "" && err.Expected != "" && err.Found != "" {
			err.Hint = fmt.Sprintf("Close %s before %s or change the closing tag.", err.Expected, err.Found)
		}
	case "template.unclosed_tag":
		if err.Hint == "" && err.Expected != "" {
			err.Hint = fmt.Sprintf("Add %s before the template ends.", err.Expected)
		}
	case "template.mismatched_closing_directive", "template.mismatched_closing_component":
		if err.Hint == "" && err.Expected != "" && err.Found != "" {
			err.Hint = fmt.Sprintf("Close %s before %s or change the closing block.", err.Expected, err.Found)
		}
	case "template.unclosed_directive", "template.unclosed_component":
		if err.Hint == "" && err.Expected != "" {
			err.Hint = fmt.Sprintf("Add %s before the template ends.", err.Expected)
		}
	}
}

func extractGoxDiagnostics(cause string) []goxDiagnostic {
	var out []goxDiagnostic
	for _, line := range strings.Split(cause, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		m := goxDiagLineRE.FindStringSubmatch(line)
		if len(m) == 5 {
			lineNo, _ := strconv.Atoi(m[2])
			column, _ := strconv.Atoi(m[3])
			out = append(out, goxDiagnostic{
				File:    normalizeGoxUserFacingPath(m[1]),
				Line:    lineNo,
				Column:  column,
				Message: strings.TrimSpace(m[4]),
			})
			continue
		}
		if m := goxGeneratedGoDiagRE.FindStringSubmatch(line); len(m) == 5 {
			lineNo, _ := strconv.Atoi(m[2])
			column, _ := strconv.Atoi(m[3])
			out = append(out, goxDiagnostic{
				File:      strings.TrimSpace(m[1]),
				Line:      lineNo,
				Column:    column,
				Message:   strings.TrimSpace(m[4]),
				Generated: true,
			})
			continue
		}
		if strings.Contains(line, ".gox:") {
			continue
		}
		out = append(out, goxDiagnostic{Message: line})
	}
	return out
}

func applyGoxDiagnosticTranslation(err *GoxError, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	if m := goxUndefinedRE.FindStringSubmatch(message); len(m) == 2 {
		name := m[1]
		err.Kind = "undefined-identifier"
		err.Message = message
		err.Hint = fmt.Sprintf("Declare %q before using it, import the package that defines it, or check for a typo.", name)
		return
	}

	if m := goxUnusedImportRE.FindStringSubmatch(message); len(m) == 2 {
		name := m[1]
		err.Kind = "unused-import"
		err.Message = message
		err.Hint = fmt.Sprintf("Remove the %q import or use it in this module.", name)
		return
	}

	if m := goxPackageNotSelectorRE.FindStringSubmatch(message); len(m) == 2 {
		name := m[1]
		err.Kind = "package-used-as-value"
		err.Message = message
		err.Hint = fmt.Sprintf("Use a selector such as %s.Value, call a function from %s, or remove it from the template expression.", name, name)
		return
	}

	if goxNoNewVariablesRE.MatchString(message) {
		err.Kind = "no-new-variables"
		err.Message = message
		err.Hint = "Use = to assign to an existing variable, or use := only when declaring at least one new variable."
		return
	}

	if message == "empty template expression" {
		err.Kind = "empty-expression"
		err.Message = "empty template expression"
		err.Hint = "Add an expression inside { ... } or remove the empty braces."
		return
	}

	if m := goxExpectedClosingTagRE.FindStringSubmatch(message); len(m) == 3 {
		err.Kind = "tag-mismatch"
		err.Expected = m[1]
		err.Found = m[2]
		err.Message = fmt.Sprintf("expected closing tag %s, got %s", m[1], m[2])
		err.Hint = fmt.Sprintf("Close %s before %s or change the closing tag.", m[1], m[2])
		return
	}

	if m := goxUndefinedFieldRE.FindStringSubmatch(message); len(m) == 3 {
		field := m[2]
		err.Kind = "undefined-field"
		err.Message = message
		if suggestion := suggestGoxErrorField(field); suggestion != "" {
			err.Hint = fmt.Sprintf("Did you mean %q?", suggestion)
		} else {
			err.Hint = fmt.Sprintf("Check the field name %q or use one of the fields exposed by ctx.Error.", field)
		}
		return
	}

	if m := goxCannotUseAsRE.FindStringSubmatch(message); len(m) == 3 {
		err.Kind = "type-mismatch"
		err.Message = message
		err.Hint = "Check the value type used in this expression and convert it to the expected type."
		err.AddDetail("type.actual", m[1])
		err.AddDetail("type.expected", m[2])
		return
	}

	err.Kind = fallbackGoxErrorKind(err)
	err.Message = message
}

var goxErrorPathLineColumnRE = regexp.MustCompile(`((?:\.?/)?(?:[A-Za-z]:)?[^:\n]*?\.gox):([0-9]+):([0-9]+)`)
var goxErrorGeneratedPathLineColumnRE = regexp.MustCompile(`((?:\.?/)?(?:[A-Za-z]:)?[^:\n]*?\.build/[^:\n]+\.go):([0-9]+):([0-9]+):\s*`)

func formatGoxErrorCause(value string) string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return value
	}

	value = goxErrorPathLineColumnRE.ReplaceAllStringFunc(value, func(match string) string {
		parts := goxErrorPathLineColumnRE.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		return normalizeGoxUserFacingPath(parts[1]) + ":" + parts[2]
	})
	value = goxErrorGeneratedPathLineColumnRE.ReplaceAllString(value, "")

	lines := strings.Split(value, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "# ") {
			continue
		}
		clean = append(clean, strings.TrimRight(line, " 	"))
	}

	return strings.Join(clean, "\n")
}

func normalizeGoxUserFacingPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}

	slash := strings.ReplaceAll(path, "\\", "/")
	slash = strings.TrimPrefix(slash, "./")

	if idx := strings.Index(slash, "/.build/pages/"); idx >= 0 {
		return goxGeneratedPagePathToSourcePath(slash[idx+len("/.build/pages/"):])
	}
	if strings.HasPrefix(slash, ".build/pages/") {
		return goxGeneratedPagePathToSourcePath(strings.TrimPrefix(slash, ".build/pages/"))
	}

	if idx := strings.Index(slash, "/pages/"); idx >= 0 {
		return slash[idx+1:]
	}
	if strings.HasPrefix(slash, "pages/") {
		return slash
	}

	return slash
}

func normalizeGoxDiagnosticFile(err *GoxError, path string) string {
	path = normalizeGoxUserFacingPath(path)
	if path == "" || !filepath.IsAbs(path) || err == nil {
		return path
	}
	root := projectRootFromGeneratedPath(err.GeneratedFile)
	if root == "" {
		return path
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(rel)
}

func goxGeneratedPagePathToSourcePath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 1 {
		return ""
	}
	routeParts := parts[:len(parts)-1]
	if len(routeParts) == 0 {
		return ""
	}
	return "pages/" + strings.Join(routeParts, "/") + ".gox"
}

func normalizeGoxErrorDetailValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.Contains(strings.ReplaceAll(value, "\\", "/"), "/pages/") || strings.HasPrefix(value, "pages/") || strings.HasPrefix(value, "./pages/") {
		return normalizeGoxUserFacingPath(value)
	}
	return value
}

func logGoxError(source string, err *GoxError) {
	if err == nil {
		return
	}
	logs.Error("\n%s", formatGoxErrorConsole(source, err))
}

func shouldLogGoxRequestError(err *GoxError) bool {
	if err == nil {
		return false
	}
	err.EnsureStatus()
	if err.StatusCode == http.StatusMethodNotAllowed {
		return false
	}
	return true
}

func formatGoxErrorConsole(source string, err *GoxError) string {
	box := newGoxErrorConsoleBox(goxRuntimeErrorConsoleTitle(source, err))

	box.Field("File", normalizeGoxUserFacingPath(err.File))
	if err.Line > 0 {
		box.Field("Line", strconv.Itoa(err.Line))
	}
	if err.Column > 0 {
		box.Field("Column", strconv.Itoa(err.Column))
	}
	box.Field("Kind", err.Kind)

	message := strings.TrimSpace(err.Message)
	if message == "" && err.Cause != nil {
		message = firstGoxErrorCauseLine(err.Cause.Error())
	}
	box.Section("Message", message)
	box.Section("Hint", err.Hint)
	return box.String()
}

func goxRuntimeErrorConsoleTitle(source string, err *GoxError) string {
	if isGoxErrorRuntimeFile(err) || source == "errorHandler" {
		return "Gox Error Handler " + goxErrorHandlerTitleSuffix(err)
	}
	suffix := goxErrorTitleSuffix(err)
	switch source {
	case "request":
		return "Gox Request " + suffix
	case "hotReload":
		return "Gox Hot Reload " + suffix
	default:
		return "Gox " + suffix
	}
}

func isGoxErrorRuntimeFile(err *GoxError) bool {
	if err == nil {
		return false
	}
	return normalizeGoxUserFacingPath(err.File) == goxErrorRelPath
}

func goxErrorTitleSuffix(err *GoxError) string {
	if err == nil || err.StatusCode == 0 {
		return "Error"
	}
	return "Error" + strconv.Itoa(err.StatusCode)
}

func goxErrorHandlerTitleSuffix(err *GoxError) string {
	if err != nil && err.Details != nil {
		if handler := strings.TrimSpace(err.Details["error.handler"]); handler != "" {
			return handler
		}
	}
	if inferred := inferGoxErrorHandlerNameFromSource(err); inferred != "" {
		return inferred
	}
	if isGoxErrorRuntimeFile(err) {
		return "Default"
	}
	return goxErrorTitleSuffix(err)
}

var goxErrorHandlerFuncRE = regexp.MustCompile(`^\s*func\s+(Default|Error[0-9]{3})\s*\(`)

func inferGoxErrorHandlerNameFromSource(err *GoxError) string {
	if err == nil || !isGoxErrorRuntimeFile(err) || err.Line <= 0 {
		return ""
	}
	sourcePath := goxErrorRuntimeSourcePath(err)
	if sourcePath == "" {
		return ""
	}
	data, readErr := os.ReadFile(sourcePath)
	if readErr != nil {
		return ""
	}

	current := ""
	for idx, line := range strings.Split(string(data), "\n") {
		lineNo := idx + 1
		if lineNo > err.Line {
			break
		}
		if match := goxErrorHandlerFuncRE.FindStringSubmatch(line); len(match) == 2 {
			current = match[1]
		}
	}
	return current
}

func goxErrorRuntimeSourcePath(err *GoxError) string {
	if err == nil {
		return ""
	}
	if filepath.IsAbs(err.File) {
		return err.File
	}
	if err.GeneratedFile != "" {
		if root := projectRootFromGeneratedPath(err.GeneratedFile); root != "" {
			return filepath.Join(root, filepath.FromSlash(goxErrorRelPath))
		}
	}
	return filepath.FromSlash(goxErrorRelPath)
}

func projectRootFromGeneratedPath(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	needle := string(filepath.Separator) + ".build" + string(filepath.Separator)
	idx := strings.Index(clean, needle)
	if idx < 0 {
		return ""
	}
	return clean[:idx]
}

func summarizeOriginalGoxError(err *GoxError) string {
	if err == nil {
		return ""
	}
	var lines []string
	location := normalizeGoxUserFacingPath(err.File)
	if err.Line > 0 {
		location = location + ":" + strconv.Itoa(err.Line)
		if err.Column > 0 {
			location = location + ":" + strconv.Itoa(err.Column)
		}
	}
	if location != "" {
		lines = append(lines, location)
	}
	message := strings.TrimSpace(err.Message)
	if message == "" && err.Cause != nil {
		message = firstGoxErrorCauseLine(err.Cause.Error())
	}
	if message != "" {
		lines = append(lines, message)
	}
	return strings.Join(lines, "\n")
}

func suggestGoxErrorField(field string) string {
	candidates := []string{
		"StatusCode",
		"StatusMessage",
		"Message",
		"Kind",
		"Hint",
		"File",
		"Line",
		"Route",
		"Method",
		"Expected",
		"Found",
	}
	best := ""
	bestDistance := 1000
	for _, candidate := range candidates {
		distance := levenshteinDistance(strings.ToLower(field), strings.ToLower(candidate))
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	if bestDistance <= 3 {
		return best
	}
	return ""
}

func levenshteinDistance(a string, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = minInt(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func minInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
}

func firstGoxErrorCauseLine(value string) string {
	value = formatGoxErrorCause(value)
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

type goxErrorConsoleBox struct {
	title string
	rows  []goxErrorConsoleRow
}

type goxErrorConsoleRow struct {
	kind  string
	key   string
	value string
}

const (
	goxErrorConsoleMinWidth = 48
	goxErrorConsoleMaxWidth = 120
)

func newGoxErrorConsoleBox(title string) *goxErrorConsoleBox {
	return &goxErrorConsoleBox{title: title}
}

func (b *goxErrorConsoleBox) Field(key string, value string) {
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	b.rows = append(b.rows, goxErrorConsoleRow{kind: "field", key: key, value: value})
}

func (b *goxErrorConsoleBox) Section(key string, value string) {
	value = strings.TrimRight(value, "\n")
	if key == "" || value == "" {
		return
	}
	b.rows = append(b.rows, goxErrorConsoleRow{kind: "section", key: key, value: value})
}

func (b *goxErrorConsoleBox) String() string {
	width := b.width()
	var out strings.Builder
	writeGoxBoxTop(&out, width)
	writeGoxBoxLine(&out, width, b.title)
	writeGoxBoxSeparator(&out, width)
	for i, row := range b.rows {
		if i > 0 && row.kind == "section" {
			writeGoxBoxSeparator(&out, width)
		}
		switch row.kind {
		case "section":
			writeGoxBoxLine(&out, width, row.key+":")
			for _, line := range splitGoxErrorLines(row.value) {
				for _, wrapped := range wrapGoxErrorText(line, width-4) {
					writeGoxBoxLine(&out, width, "  "+wrapped)
				}
			}
		default:
			prefix := row.key + ": "
			available := width - visibleLen(prefix)
			wrapped := wrapGoxErrorText(row.value, available)
			if len(wrapped) == 0 {
				writeGoxBoxLine(&out, width, prefix)
				continue
			}
			writeGoxBoxLine(&out, width, prefix+wrapped[0])
			indent := strings.Repeat(" ", visibleLen(prefix))
			for _, line := range wrapped[1:] {
				writeGoxBoxLine(&out, width, indent+line)
			}
		}
	}
	writeGoxBoxBottom(&out, width)
	return strings.TrimRight(out.String(), "\n")
}

func (b *goxErrorConsoleBox) width() int {
	width := visibleLen(b.title)
	for _, row := range b.rows {
		switch row.kind {
		case "section":
			if n := visibleLen(row.key) + 1; n > width {
				width = n
			}
			for _, line := range splitGoxErrorLines(row.value) {
				if n := visibleLen(line) + 2; n > width {
					width = n
				}
			}
		default:
			if n := visibleLen(row.key) + 2 + visibleLen(row.value); n > width {
				width = n
			}
		}
	}
	if width < goxErrorConsoleMinWidth {
		width = goxErrorConsoleMinWidth
	}
	if width > goxErrorConsoleMaxWidth {
		width = goxErrorConsoleMaxWidth
	}
	return width
}

func writeGoxBoxTop(b *strings.Builder, width int) {
	b.WriteString("╭")
	b.WriteString(strings.Repeat("─", width+2))
	b.WriteString("╮\n")
}

func writeGoxBoxSeparator(b *strings.Builder, width int) {
	b.WriteString("├")
	b.WriteString(strings.Repeat("─", width+2))
	b.WriteString("┤\n")
}

func writeGoxBoxBottom(b *strings.Builder, width int) {
	b.WriteString("╰")
	b.WriteString(strings.Repeat("─", width+2))
	b.WriteString("╯\n")
}

func writeGoxBoxLine(b *strings.Builder, width int, text string) {
	text = strings.TrimRight(text, "\n")
	padding := width - visibleLen(text)
	if padding < 0 {
		padding = 0
	}
	b.WriteString("│ ")
	b.WriteString(text)
	b.WriteString(strings.Repeat(" ", padding))
	b.WriteString(" │\n")
}

func splitGoxErrorLines(value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

func wrapGoxErrorText(value string, max int) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{""}
	}
	if max < 16 {
		max = 16
	}
	var out []string
	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		for visibleLen(line) > max {
			cut := findGoxErrorWrapCut(line, max)
			out = append(out, strings.TrimRight(line[:cut], " "))
			line = strings.TrimLeft(line[cut:], " ")
			if line == "" {
				break
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func findGoxErrorWrapCut(value string, max int) int {
	lastSpace := -1
	count := 0
	for idx, r := range value {
		if count >= max {
			if lastSpace > 0 {
				return lastSpace + 1
			}
			if idx > 0 {
				return idx
			}
			return len(string(r))
		}
		if r == ' ' || r == '\t' {
			lastSpace = idx
		}
		count++
	}
	return len(value)
}

func visibleLen(value string) int {
	return len([]rune(value))
}

func writeGoxErrorLine(b *strings.Builder, key string, value string) {
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func writeIndentedBlock(b *strings.Builder, value string) {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return
	}
	for _, line := range strings.Split(value, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
}
