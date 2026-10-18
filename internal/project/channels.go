package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/goxsource"
)

// ChannelEntry is one loadable channel definition. Name identifies the
// reusable definition; dynamic instance keys are supplied at runtime.
type ChannelEntry struct {
	Name     string
	File     string
	Function string
}

// Channels builds the lightweight startup index for websocket channel
// definitions. It reads paths only; source modules are compiled on demand.
func (p *Project) Channels() ([]ChannelEntry, error) {
	root := filepath.Join(p.RootDir, "websocket")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return []ChannelEntry{}, nil
	}

	entries := []ChannelEntry{}
	seen := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".gox" {
			return nil
		}
		relProject, err := filepath.Rel(p.RootDir, path)
		if err != nil {
			return err
		}
		relProject = filepath.ToSlash(relProject)
		context := goxsource.ClassifyModule(relProject)
		if context.Kind != goxsource.ModulePublicChannel {
			return nil
		}
		name := context.ChannelName
		if previous, exists := seen[name]; exists {
			return fmt.Errorf("duplicate channel %s: %s and %s", name, previous, relProject)
		}
		seen[name] = relProject
		entries = append(entries, ChannelEntry{Name: name, File: relProject, Function: context.ChannelFunction})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
