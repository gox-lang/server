package clientbuilder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/clienttypes"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
)

type FrameworkWorkspace struct {
	Framework       string
	RootDir         string
	PackageJSONPath string
	Changed         bool
}

type packageJSON struct {
	Private         bool
	Type            string
	Dependencies    map[string]string
	DevDependencies map[string]string
	Extra           map[string]json.RawMessage
}

func (p *packageJSON) UnmarshalJSON(data []byte) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	p.Extra = fields
	if raw, ok := fields["private"]; ok {
		if err := json.Unmarshal(raw, &p.Private); err != nil {
			return fmt.Errorf("private: %w", err)
		}
		delete(p.Extra, "private")
	}
	if raw, ok := fields["type"]; ok {
		if err := json.Unmarshal(raw, &p.Type); err != nil {
			return fmt.Errorf("type: %w", err)
		}
		delete(p.Extra, "type")
	}
	if raw, ok := fields["dependencies"]; ok {
		if err := json.Unmarshal(raw, &p.Dependencies); err != nil {
			return fmt.Errorf("dependencies: %w", err)
		}
		delete(p.Extra, "dependencies")
	}
	if raw, ok := fields["devDependencies"]; ok {
		if err := json.Unmarshal(raw, &p.DevDependencies); err != nil {
			return fmt.Errorf("devDependencies: %w", err)
		}
		delete(p.Extra, "devDependencies")
	}
	return nil
}

func (p packageJSON) MarshalJSON() ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(p.Extra)+4)
	for key, value := range p.Extra {
		fields[key] = value
	}

	set := func(key string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		fields[key] = data
		return nil
	}
	if err := set("private", p.Private); err != nil {
		return nil, err
	}
	if err := set("type", p.Type); err != nil {
		return nil, err
	}
	if len(p.Dependencies) > 0 {
		if err := set("dependencies", p.Dependencies); err != nil {
			return nil, err
		}
	} else {
		delete(fields, "dependencies")
	}
	if len(p.DevDependencies) > 0 {
		if err := set("devDependencies", p.DevDependencies); err != nil {
			return nil, err
		}
	} else {
		delete(fields, "devDependencies")
	}
	return json.Marshal(fields)
}

func EnsureFrameworkWorkspaces(projectRoot string, assets []Asset) ([]FrameworkWorkspace, error) {
	frameworks := uniqueFrameworks(assets)
	out := make([]FrameworkWorkspace, 0, len(frameworks))
	for _, framework := range frameworks {
		ws, err := EnsureFrameworkWorkspace(projectRoot, framework)
		if err != nil {
			return out, err
		}
		out = append(out, ws)
	}
	return out, nil
}

func EnsureFrameworkWorkspace(projectRoot string, framework string) (FrameworkWorkspace, error) {
	framework = strings.ToLower(strings.TrimSpace(framework))
	if framework == "" {
		return FrameworkWorkspace{}, fmt.Errorf("missing client framework")
	}
	managedDeps, managedDevDeps, err := ProjectManagedDependencies(projectRoot, framework)
	if err != nil {
		return FrameworkWorkspace{}, err
	}
	root := filepath.Join(projectRoot, "client", framework)
	pkgPath := filepath.Join(root, "package.json")
	if err := os.MkdirAll(root, 0755); err != nil {
		return FrameworkWorkspace{}, err
	}

	pkg := packageJSON{Private: true, Type: "module", Dependencies: map[string]string{}, DevDependencies: map[string]string{}, Extra: map[string]json.RawMessage{}}
	if data, err := os.ReadFile(pkgPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &pkg); err != nil {
			return FrameworkWorkspace{}, fmt.Errorf("invalid %s: %w", pkgPath, err)
		}
	}
	if pkg.Dependencies == nil {
		pkg.Dependencies = map[string]string{}
	}
	if pkg.DevDependencies == nil {
		pkg.DevDependencies = map[string]string{}
	}
	changed := false
	if !pkg.Private {
		pkg.Private = true
		changed = true
	}
	if strings.TrimSpace(pkg.Type) == "" {
		pkg.Type = "module"
		changed = true
	}
	for name, version := range managedDeps {
		if got, exists := pkg.Dependencies[name]; !exists || got != version {
			pkg.Dependencies[name] = version
			changed = true
		}
		if _, exists := pkg.DevDependencies[name]; exists {
			delete(pkg.DevDependencies, name)
			changed = true
		}
	}
	for name, version := range managedDevDeps {
		if got, exists := pkg.DevDependencies[name]; !exists || got != version {
			pkg.DevDependencies[name] = version
			changed = true
		}
		if _, exists := pkg.Dependencies[name]; exists {
			delete(pkg.Dependencies, name)
			changed = true
		}
	}
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		changed = true
	}
	if changed {
		data, err := marshalPackageJSON(pkg)
		if err != nil {
			return FrameworkWorkspace{}, err
		}
		if err := os.WriteFile(pkgPath, data, 0644); err != nil {
			return FrameworkWorkspace{}, err
		}
	}
	if err := ensurePNPMWorkspaceSettings(root); err != nil {
		return FrameworkWorkspace{}, err
	}
	if err := ensureTypeScriptConfig(root, framework); err != nil {
		return FrameworkWorkspace{}, err
	}
	if err := clienttypes.EnsureStatic(projectRoot); err != nil {
		return FrameworkWorkspace{}, err
	}
	return FrameworkWorkspace{Framework: framework, RootDir: root, PackageJSONPath: pkgPath, Changed: changed}, nil
}

func InstallFrameworkWorkspace(node toolchain.NodePaths, pnpm toolchain.PNPMPaths, workspace FrameworkWorkspace) error {
	if strings.TrimSpace(workspace.RootDir) == "" {
		return fmt.Errorf("missing client framework workspace root")
	}

	logs.Info("installing client framework dependencies: %s", workspace.Framework)
	cmd := toolchain.PNPMCommand(node, pnpm, workspace.RootDir, "install", "--reporter=silent", "--config.update-notifier=false")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		if text := strings.TrimSpace(out.String()); text != "" {
			logs.Error("pnpm install output:\n%s", text)
		}
		return fmt.Errorf("pnpm install failed for client/%s: %w", workspace.Framework, err)
	}

	logs.Info("client framework dependencies installed: %s", workspace.Framework)
	return nil
}

func ensureTypeScriptConfig(root, framework string) error {
	def, ok := Framework(framework)
	if !ok {
		return nil
	}
	path := filepath.Join(root, "tsconfig.json")
	config := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("invalid %s: %w", path, err)
		}
	}
	compiler, _ := config["compilerOptions"].(map[string]any)
	if compiler == nil {
		compiler = map[string]any{}
	}
	for key, value := range def.TypeScriptOptions {
		if _, exists := compiler[key]; !exists {
			compiler[key] = value
		}
	}
	if _, exists := compiler["noEmit"]; !exists {
		compiler["noEmit"] = true
	}
	config["compilerOptions"] = compiler
	include := stringSlice(config["include"])
	include = appendUnique(include, "**/*", "../../.build/*.d.ts")
	config["include"] = include
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0644)
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if v, ok := value.([]string); ok {
			return append([]string(nil), v...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
func appendUnique(values []string, additions ...string) []string {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	for _, v := range additions {
		if !seen[v] {
			values = append(values, v)
			seen[v] = true
		}
	}
	return values
}

func ensurePNPMWorkspaceSettings(root string) error {
	path := filepath.Join(root, "pnpm-workspace.yaml")
	content := []byte("onlyBuiltDependencies:\n  - esbuild\n")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return nil
	}
	return os.WriteFile(path, content, 0644)
}

func uniqueFrameworks(assets []Asset) []string {
	seen := map[string]bool{}
	for _, asset := range assets {
		fw := strings.ToLower(strings.TrimSpace(asset.Framework))
		if fw != "" {
			seen[fw] = true
		}
	}
	out := make([]string, 0, len(seen))
	for fw := range seen {
		out = append(out, fw)
	}
	sort.Strings(out)
	return out
}

func marshalPackageJSON(pkg packageJSON) ([]byte, error) {
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
