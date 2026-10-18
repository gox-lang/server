package clientbuilder

import (
	"bytes"
	"os"
	"path/filepath"
)

type WrittenConfig struct {
	Asset Asset
	Path  string
}

func WriteViteConfigs(assets []Asset) ([]WrittenConfig, error) {
	written := make([]WrittenConfig, 0, len(assets))
	for _, asset := range assets {
		configPath := asset.ViteConfigPath()
		if configPath == "" {
			continue
		}
		content, err := GenerateViteConfig(asset)
		if err != nil {
			return written, err
		}
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			return written, err
		}
		if current, err := os.ReadFile(configPath); err == nil && bytes.Equal(current, content) {
			written = append(written, WrittenConfig{Asset: asset, Path: configPath})
			continue
		}
		if err := os.WriteFile(configPath, content, 0644); err != nil {
			return written, err
		}
		written = append(written, WrittenConfig{Asset: asset, Path: configPath})
	}
	return written, nil
}
