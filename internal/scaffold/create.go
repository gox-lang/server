package scaffold

import (
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/logs"
)

func CreateProject(ctx cli.Context, opts Options) error {
	logs.Info("creating Gox project: %s", opts.ProjectName)

	if err := ValidateProjectName(opts.ProjectName); err != nil {
		return err
	}

	projectRoot := opts.ProjectPath()
	logs.Info("project path: %s", projectRoot)

	if err := EnsureTargetDoesNotExist(projectRoot); err != nil {
		return err
	}

	logs.Info("writing directories...")
	if err := WriteDirs(projectRoot, ProjectDirs()); err != nil {
		return err
	}

	logs.Info("writing files...")
	if err := WriteFiles(projectRoot, ProjectFiles(opts)); err != nil {
		return err
	}

	logs.Info("verifying project structure...")
	if err := VerifyProject(projectRoot, opts); err != nil {
		return err
	}

	logs.Info("created Gox project: %s", opts.ProjectName)

	return nil
}
