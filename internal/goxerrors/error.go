package goxerrors

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/gox-lang/gox"
)

// Type identifies the subsystem that produced a Gox error.
type Type string

const (
	TypeCompiler Type = "compiler"
	TypeTemplate Type = "template"
	TypeRuntime  Type = "runtime"
	TypeBuild    Type = "build"
	TypeLoad     Type = "load"
	TypeResponse Type = "response"
	TypeFlow     Type = "flow"
	TypeHTTP     Type = "http"
	TypePanic    Type = "panic"
)

// GoxError is the single internal error model used by compiler, template, build,
// load and runtime layers. Public SDK access must continue going through
// github.com/gox-lang/gox.Error.
type GoxError struct {
	Type  Type
	Stage gox.GoxErrorStage
	Kind  string

	Route    string
	Method   string
	IsAction bool

	File          string
	Line          int
	Column        int
	GeneratedFile string
	PluginPath    string

	Message  string
	Hint     string
	Expected string
	Found    string

	Cause error
	Stack string

	OriginalError *GoxError

	Status        int
	StatusCode    int
	StatusMessage string
	Details       map[string]string
}

func New(typ Type, stage gox.GoxErrorStage, message string, cause error) *GoxError {
	err := &GoxError{
		Type:       typ,
		Stage:      stage,
		Message:    message,
		Cause:      cause,
		Status:     http.StatusInternalServerError,
		StatusCode: http.StatusInternalServerError,
	}
	err.EnsureStatus()
	return err
}

func NewFromStage(stage gox.GoxErrorStage, message string, cause error) *GoxError {
	return New(TypeFromStage(stage), stage, message, cause)
}

func NewPanic(recovered any) *GoxError {
	err := New(TypePanic, gox.GoxErrorPanic, fmt.Sprint(recovered), nil)
	err.Stack = string(debug.Stack())
	return err
}

func TypeFromStage(stage gox.GoxErrorStage) Type {
	switch stage {
	case gox.GoxErrorCompile:
		return TypeCompiler
	case gox.GoxErrorBuild:
		return TypeBuild
	case gox.GoxErrorLoad:
		return TypeLoad
	case gox.GoxErrorWriter:
		return TypeResponse
	case gox.GoxErrorPanic:
		return TypePanic
	case gox.GoxErrorDispatch:
		return TypeRuntime
	case gox.GoxErrorResolve:
		return TypeHTTP
	default:
		return TypeRuntime
	}
}

func (e *GoxError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" && e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	if e.Kind != "" {
		return "gox error: " + e.Kind
	}
	if e.Type != "" {
		return "gox error: " + string(e.Type)
	}
	return "gox error"
}

func (e *GoxError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *GoxError) AddDetail(key string, value string) *GoxError {
	if e == nil || key == "" {
		return e
	}
	if e.Details == nil {
		e.Details = make(map[string]string)
	}
	e.Details[key] = value
	return e
}

func (e *GoxError) EnsureStatus() {
	if e == nil {
		return
	}
	if e.Status == 0 && e.StatusCode != 0 {
		e.Status = e.StatusCode
	}
	if e.StatusCode == 0 && e.Status != 0 {
		e.StatusCode = e.Status
	}
	if e.Status == 0 {
		e.Status = http.StatusInternalServerError
	}
	if e.StatusCode == 0 {
		e.StatusCode = e.Status
	}
	if strings.TrimSpace(e.StatusMessage) == "" {
		e.StatusMessage = http.StatusText(e.Status)
		if e.StatusMessage == "" {
			e.StatusMessage = "Error"
		}
	}
}

func (e *GoxError) CloneDetails() map[string]string {
	if e == nil || len(e.Details) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.Details))
	for key, value := range e.Details {
		out[key] = value
	}
	return out
}

func (e *GoxError) GetStage() gox.GoxErrorStage {
	if e == nil {
		return ""
	}
	return e.Stage
}

func (e *GoxError) GetRoute() string {
	if e == nil {
		return ""
	}
	return e.Route
}

func (e *GoxError) GetMethod() string {
	if e == nil {
		return ""
	}
	return e.Method
}

func (e *GoxError) GetIsAction() bool {
	if e == nil {
		return false
	}
	return e.IsAction
}

func (e *GoxError) GetFile() string {
	if e == nil {
		return ""
	}
	return e.File
}

func (e *GoxError) GetLine() int {
	if e == nil {
		return 0
	}
	return e.Line
}

func (e *GoxError) GetColumn() int {
	if e == nil {
		return 0
	}
	return e.Column
}

func (e *GoxError) GetGeneratedFile() string {
	if e == nil {
		return ""
	}
	return e.GeneratedFile
}

func (e *GoxError) GetPluginPath() string {
	if e == nil {
		return ""
	}
	return e.PluginPath
}

func (e *GoxError) GetKind() string {
	if e == nil {
		return ""
	}
	return e.Kind
}

func (e *GoxError) GetMessage() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *GoxError) GetHint() string {
	if e == nil {
		return ""
	}
	return e.Hint
}

func (e *GoxError) GetExpected() string {
	if e == nil {
		return ""
	}
	return e.Expected
}

func (e *GoxError) GetFound() string {
	if e == nil {
		return ""
	}
	return e.Found
}

func (e *GoxError) GetCause() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *GoxError) GetStack() string {
	if e == nil {
		return ""
	}
	return e.Stack
}

func (e *GoxError) GetOriginalError() gox.Error {
	if e == nil || e.OriginalError == nil {
		return nil
	}
	return e.OriginalError
}

func (e *GoxError) GetStatusCode() int {
	if e == nil {
		return 0
	}
	e.EnsureStatus()
	return e.StatusCode
}

func (e *GoxError) GetStatusMessage() string {
	if e == nil {
		return ""
	}
	e.EnsureStatus()
	return e.StatusMessage
}

func (e *GoxError) GetDetails() map[string]string {
	return e.CloneDetails()
}

var _ gox.Error = (*GoxError)(nil)
