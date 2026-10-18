package scaffold

import (
	"path/filepath"

	"github.com/gox-lang/server/internal/deps"
)

type Options struct {
	ProjectName      string
	TargetDir        string
	GoVersion        string
	GoxModule        string
	GoxModuleVersion string
	Manifest         deps.GoxJSON
}

func (o Options) ProjectPath() string {
	return filepath.Join(o.TargetDir, o.ProjectName)
}
