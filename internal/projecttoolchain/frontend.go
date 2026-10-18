package projecttoolchain

import (
	"fmt"
	"strings"

	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/toolchain"
)

type Frontend struct {
	Node toolchain.NodeInfo
	PNPM toolchain.PNPMInfo
}

func Resolve(root string) (Frontend, error) {
	manifest, err := deps.ReadGoxJSON(root)
	if err != nil {
		return Frontend{}, err
	}

	nodeVersion := strings.TrimSpace(manifest.Toolchain.Node)
	if nodeVersion == "" {
		return Frontend{}, fmt.Errorf("%s toolchain.node is required", deps.GoxJSONFile)
	}
	pnpmVersion := strings.TrimSpace(manifest.Toolchain.PNPM)
	if pnpmVersion == "" {
		return Frontend{}, fmt.Errorf("%s toolchain.pnpm is required", deps.GoxJSONFile)
	}

	return Frontend{
		Node: toolchain.NewNode(nodeVersion),
		PNPM: toolchain.NewPNPM(pnpmVersion),
	}, nil
}

func Ensure(root string) (toolchain.NodePaths, toolchain.PNPMPaths, error) {
	frontend, err := Resolve(root)
	if err != nil {
		return toolchain.NodePaths{}, toolchain.PNPMPaths{}, err
	}

	nodePaths, err := toolchain.EnsureNode(frontend.Node)
	if err != nil {
		return toolchain.NodePaths{}, toolchain.PNPMPaths{}, err
	}
	pnpmPaths, err := toolchain.EnsurePNPM(frontend.PNPM, nodePaths)
	if err != nil {
		return toolchain.NodePaths{}, toolchain.PNPMPaths{}, err
	}
	return nodePaths, pnpmPaths, nil
}
