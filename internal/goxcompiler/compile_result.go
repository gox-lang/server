package gox

import "time"

type CompileResult struct {
	EntryPath     string
	EntryAbsPath  string
	OutPath       string
	OutAbsPath    string
	SourceMapPath string

	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration

	LoadGraphDuration time.Duration
	BundleDuration    time.Duration
	WriteDuration     time.Duration

	GoPackage       string
	ModuleCount     int
	DependencyCount int
	OutputBytes     int64
	Written         bool

	Dependencies           []CompileDependency
	GoPackages             []CompileGoPackage
	ResolutionDependencies []string
	Modules                []CompileModule
	ClientComponents       []CompileClientComponent
	ClientBundles          []CompileClientBundle
	ClientAssetMap         map[string]string
	ClientRuntimeURL       string
	ClientBaseStyleURL     string
	ActionsRuntimeURL      string
}

// CompileGoPackage describes a local Go package participating in a compile.
// ImportPath identifies the package as Go code imports it, while Dir and Files
// identify its physical sources in the project.
type CompileGoPackage struct {
	ImportPath string
	Dir        string
	Files      []string
	Imports    []string
	ImportedBy []string
	Direct     bool
}

type CompileDependency struct {
	Path               string
	AbsPath            string
	ModuleName         string
	DeclaredModuleName string
	IsEntry            bool

	Imports    []string
	GoImports  []string
	ImportedBy []string

	Size         int64
	ModTime      time.Time
	LoadDuration time.Duration
}

type CompileModule struct {
	Path               string
	AbsPath            string
	ModuleName         string
	DeclaredModuleName string
	IsEntry            bool

	Imports      []string
	GoImports    []string
	Clients      []CompileClientComponent
	ImportedBy   []string
	LoadDuration time.Duration
}

type CompileClientComponent struct {
	Name          string `json:"name"`
	Key           string `json:"key"`
	Framework     string `json:"framework"`
	Entry         string `json:"entry"`
	Path          string `json:"path"`
	AbsPath       string `json:"-"`
	ModulePath    string `json:"module"`
	ModuleAbsPath string `json:"-"`
	Dev           bool
	Mode          string
	OutputDir     string
}

type CompileClientBundle struct {
	Name      string
	Framework string
	Entry     string
	Path      string
	Mode      string

	EntryURL  string
	EntryFile string

	Assets []CompileClientAsset
	Graph  CompileClientDependencyGraph
}

type CompileClientAsset struct {
	Kind string
	URL  string
	File string
}

type CompileClientDependencyGraph struct {
	Entry          string
	EntryFile      string
	Framework      string
	Component      string
	OutputFiles    []string
	ModuleFiles    []string
	Imports        []string
	DynamicImports []string
	Nodes          []CompileClientDependencyGraphNode
}

type CompileClientDependencyGraphNode struct {
	File           string
	Imports        []string
	DynamicImports []string
}
