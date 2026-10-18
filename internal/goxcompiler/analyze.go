package gox

import (
	"path/filepath"
	"time"
)

// Analyze loads and resolves a .gox module graph without generating Go output.
func Analyze(entryPath string) (*CompileResult, error) {
	startedAt := time.Now()

	result := &CompileResult{
		EntryPath: entryPath,
		StartedAt: startedAt,
	}

	if absEntry, err := filepath.Abs(entryPath); err == nil {
		result.EntryAbsPath = absEntry
	}

	defer func() {
		result.FinishedAt = time.Now()
		result.Duration = result.FinishedAt.Sub(result.StartedAt)
	}()

	loadStart := time.Now()
	graph, err := LoadGraph(entryPath)
	result.LoadGraphDuration = time.Since(loadStart)
	if err != nil {
		return result, err
	}

	if err := validateProjectGoGoroutineSafety(graph.RootDir); err != nil {
		return result, err
	}

	result.Dependencies = buildCompileDependencies(graph)
	result.GoPackages, err = resolveCompileGoPackages(graph)
	if err != nil {
		return result, err
	}
	result.ResolutionDependencies = append([]string(nil), graph.ResolutionDependencies...)
	result.Modules = buildCompileModules(graph)
	result.ClientComponents = buildCompileClientComponents(graph)
	result.ModuleCount = len(graph.Modules)
	result.DependencyCount = len(result.Dependencies)
	result.Written = false

	if graph.Entry != nil {
		result.GoPackage = graph.Entry.GoPackage
	}

	return result, nil
}
