package deps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const GoxJSONFile = "gox.json"

type Toolchain struct {
	Node       string `json:"node"`
	PNPM       string `json:"pnpm"`
	Vite       string `json:"vite"`
	TypeScript string `json:"typescript"`
}

type Frameworks map[string]map[string]string

type GoxJSON struct {
	Dependencies map[string]string `json:"dependencies"`
	Toolchain    Toolchain         `json:"toolchain"`
	Frameworks   Frameworks        `json:"frameworks"`
}

func NewGoxJSON() GoxJSON {
	return GoxJSON{
		Dependencies: map[string]string{},
		Frameworks:   Frameworks{},
	}
}

func ReadGoxJSON(root string) (GoxJSON, error) {
	path := filepath.Join(root, GoxJSONFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewGoxJSON(), nil
		}
		return GoxJSON{}, err
	}
	var cfg GoxJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return GoxJSON{}, fmt.Errorf("read %s: %w", GoxJSONFile, err)
	}
	if cfg.Dependencies == nil {
		cfg.Dependencies = map[string]string{}
	}
	if cfg.Frameworks == nil {
		cfg.Frameworks = Frameworks{}
	}
	return cfg, nil
}

func WriteGoxJSON(root string, cfg GoxJSON) error {
	if cfg.Dependencies == nil {
		cfg.Dependencies = map[string]string{}
	}
	if cfg.Frameworks == nil {
		cfg.Frameworks = Frameworks{}
	}

	data, err := marshalStableJSON(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, GoxJSONFile), data, 0o644)
}

func AddDependency(root string, module string, version string) error {
	if module == "" {
		return fmt.Errorf("dependency package is required")
	}
	if version == "" {
		version = "latest"
	}
	cfg, err := ReadGoxJSON(root)
	if err != nil {
		return err
	}
	cfg.Dependencies[module] = version
	return WriteGoxJSON(root, cfg)
}

func RemoveDependency(root string, module string) error {
	if module == "" {
		return fmt.Errorf("dependency package is required")
	}
	cfg, err := ReadGoxJSON(root)
	if err != nil {
		return err
	}
	delete(cfg.Dependencies, module)
	return WriteGoxJSON(root, cfg)
}

func SortedDependencies(cfg GoxJSON) []Dependency {
	deps := make([]Dependency, 0, len(cfg.Dependencies))
	for path, version := range cfg.Dependencies {
		deps = append(deps, Dependency{Path: path, Version: version})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })
	return deps
}

type Dependency struct {
	Path    string
	Version string
}

func marshalStableJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(data)
	out.WriteByte('\n')
	return out.Bytes(), nil
}
