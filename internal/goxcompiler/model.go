package gox

import (
	"time"

	"go/ast"
	"go/token"

	"github.com/gox-lang/server/internal/goxmap"
	"github.com/gox-lang/server/internal/goxsource"
)

type ModuleGraph struct {
	Entry                  *Module
	Modules                []*Module
	ByPath                 map[string]*Module
	RootDir                string
	ResolutionDependencies []string
}

type Module struct {
	Path         string
	Name         string
	DeclaredName string
	GoPackage    string
	Structure    goxsource.ModuleContext

	// File/Origins are the runtime/compiler representation and may be mutated
	// by GOX lowering after parsing. Editor type semantics live in Semantic.
	File   *ast.File
	Source string

	ParseSource           string
	ParseSourceMap        *goxmap.Map
	Origins               *OriginTable
	Semantic              *SemanticModel
	GoSemanticTokens      []SemanticToken
	Symbols               []SourceSymbol
	LocalSymbols          []LocalSymbol
	SelectorReferences    []SelectorReference
	UnresolvedIdentifiers []UnresolvedIdentifier
	References            []SourceReference

	GoImports  []ImportDecl
	GoxImports []GoxImport
	Assets     []AssetImport

	Vars   []VarDecl
	Consts []ConstDecl
	Types  []TypeDecl
	Funcs  []FuncDecl

	Clients             []ClientDecl
	ClientSemantics     []ClientSemantic
	Templates           []TemplateReturn
	TemplateOccurrences []TemplateOccurrence
	ComponentCalls      []pendingComponentCall
	WebSocketContracts  []WebSocketContract

	LoadDuration time.Duration
}

type AssetKind string

const (
	AssetCSS AssetKind = "css"
	AssetTS  AssetKind = "ts"
	AssetMTS AssetKind = "mts"
	AssetSVG AssetKind = "svg"
)

type AssetImport struct {
	Path  string
	Alias string
	Kind  AssetKind

	Spec *ast.ImportSpec
}

// =========================
// IMPORTS
// =========================

type ImportDecl struct {
	Path  string // "fmt", "net/http"
	Alias string // "", ".", "_", "json"

	Spec *ast.ImportSpec

	SourceFile string
	SourceLine int
	Source     goxmap.SourceRange
}

type GoxImport struct {
	Path   string // "./buttons.gox"
	Name   string // Button, Ux, Layout, etc.
	Source goxmap.SourceRange

	ResolvedPath         string
	ResolutionCandidates []string
}

// =========================
// VARS
// =========================

type VarDecl struct {
	Name string

	Type  ast.Expr
	Value ast.Expr

	Spec *ast.ValueSpec
}

// =========================
// CONSTS
// =========================

type ConstDecl struct {
	Name string

	Type  ast.Expr
	Value ast.Expr

	Spec *ast.ValueSpec
}

// =========================
// TYPES
// =========================

type TypeDecl struct {
	Name string

	Node *ast.TypeSpec
}

// =========================
// FUNCS
// =========================

type FuncKind string

const (
	FuncNormal    FuncKind = "normal"
	FuncPage      FuncKind = "page"
	FuncHandler   FuncKind = "handler"
	FuncComponent FuncKind = "component"
	FuncAPI       FuncKind = "api"
	FuncJSON      FuncKind = "json"
	FuncFile      FuncKind = "file"
	FuncText      FuncKind = "text"
	FuncAction    FuncKind = "action"
	FuncConfig    FuncKind = "config"
	FuncWebSocket FuncKind = "websocket"
	FuncChannel   FuncKind = "channel"
)

type FuncDecl struct {
	Name string
	Kind FuncKind

	Node *ast.FuncDecl

	Params            []flatParam
	ActionReturnTypes []string
}

// =========================
// CLIENT COMPONENTS
// =========================

type ClientFramework string

const (
	ClientGox    ClientFramework = "Gox"
	ClientReact  ClientFramework = "React"
	ClientPreact ClientFramework = "Preact"
	ClientSolid  ClientFramework = "Solid"
	ClientVue    ClientFramework = "Vue"
	ClientSvelte ClientFramework = "Svelte"
)

type ClientDecl struct {
	Name         string
	Key          string
	Framework    ClientFramework
	Entry        string
	EntryPath    string
	EntryAbsPath string
	Dev          bool

	Pos         token.Pos
	NamePos     token.Pos
	EntryPos    token.Pos
	EntryOffset int

	SourceOffset    int
	NameOffset      int
	FrameworkOffset int
	SrcKeyOffset    int
	DevKeyOffset    int
	DevValueOffset  int
	DevDeclared     bool
}

type ClientSemantic struct {
	Source goxmap.SourceRange
	Detail string
}

// =========================
// BUNDLE
// =========================

type Bundle struct {
	Entry   *Module
	Modules []*Module

	GoPackage string
	GoImports []ImportDecl
	Decls     []ast.Decl
	Origins   *OriginTable
	Sources   map[string]string
}
