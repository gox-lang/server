package gox

import (
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"

	"github.com/gox-lang/server/internal/goxmap"
)

// SemanticDependencyKind identifies the workspace namespace resolved by one
// source import during semantic analysis.
type SemanticDependencyKind string

const (
	SemanticDependencyGoPackage SemanticDependencyKind = "go-package"
	SemanticDependencyGoxModule SemanticDependencyKind = "gox-module"
)

type SemanticDependency struct {
	// SourcePath is the .gox module that owns this import edge. Keeping the
	// importer on every edge lets the next phase build a reverse dependency
	// graph without rediscovering imports or flattening GOX module chains.
	SourcePath           string
	Kind                 SemanticDependencyKind
	ImportPath           string
	PackagePath          string
	ResolvedPath         string
	ResolutionCandidates []string
}

// SemanticModel is the source-level Go semantic representation of a .gox
// module. It owns an AST that is intentionally independent from Module.File:
// Module.File is free to undergo runtime/compiler rewrites while File remains
// the stable tree used as keys in Info. Positions belong to FileSet and source
// provenance belongs to Origins/SourceMap from the semantic projection.
type SemanticModel struct {
	File      *ast.File
	FileSet   *token.FileSet
	Origins   *OriginTable
	SourceMap *goxmap.Map

	Package      *types.Package
	Info         *types.Info
	Errors       []error
	Dependencies []SemanticDependency
}

// buildSemanticModel keeps the small AST-only helper used by focused tests and
// callers that do not need source provenance. Production modules should use
// buildSemanticModelFromProjection so File, Origins and SourceMap remain one
// coherent semantic snapshot.
func buildSemanticModel(file *ast.File, fset *token.FileSet) *SemanticModel {
	return buildSemanticModelWithSource(file, fset, nil, nil)
}

func buildSemanticModelFromProjection(projection *SemanticProjection) *SemanticModel {
	if projection == nil {
		return nil
	}
	return buildSemanticModelWithSource(projection.File, projection.FileSet, projection.Origins, projection.SourceMap)
}

func buildSemanticModelWithSource(file *ast.File, fset *token.FileSet, origins *OriginTable, sourceMap *goxmap.Map) *SemanticModel {
	return buildSemanticModelWithImporter(file, fset, origins, sourceMap, "", importer.Default())
}

func buildSemanticModelWithImporter(file *ast.File, fset *token.FileSet, origins *OriginTable, sourceMap *goxmap.Map, packagePath string, semanticImporter types.Importer) *SemanticModel {
	if file == nil || fset == nil {
		return nil
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	model := &SemanticModel{
		File:      file,
		FileSet:   fset,
		Origins:   origins,
		SourceMap: sourceMap,
		Info:      info,
	}
	if semanticImporter == nil {
		semanticImporter = importer.Default()
	}
	if packagePath == "" {
		packagePath = file.Name.Name
	}
	configuration := types.Config{
		Importer: semanticImporter,
		Error: func(err error) {
			model.Errors = append(model.Errors, err)
		},
	}
	model.Package, _ = configuration.Check(packagePath, fset, []*ast.File{file}, info)
	return model
}
