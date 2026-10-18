package clientbuilder

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func GenerateViteConfig(asset Asset) ([]byte, error) {
	framework := strings.ToLower(strings.TrimSpace(asset.Framework))
	pluginSetup, pluginExprs, err := vitePlugin(framework)
	if err != nil {
		return nil, err
	}

	profile := asset.BuildProfile()
	packageJSONPath := filepath.Join(asset.EffectiveRoot(), "package.json")
	imports := `import { createRequire } from "node:module"` + "\n"
	imports += fmt.Sprintf("const require = createRequire(%s)\n", jsString(packageJSONPath))
	imports += `const { defineConfig } = await import(require.resolve("vite"))` + "\n"
	if pluginSetup != "" {
		imports += pluginSetup + "\n"
	}
	imports += goxClientGraphPluginJS(asset) + "\n"

	plugins := "[goxClientGraphPlugin()]"
	if len(pluginExprs) > 0 {
		plugins = "[" + strings.Join(append(pluginExprs, "goxClientGraphPlugin()"), ", ") + "]"
	}

	vendorSetup := goxVendorConfigJS(asset, framework, asset.Mode, asset.Dev)
	cssSetup := goxCssModuleConfigJS(asset.Name)

	cfg := fmt.Sprintf(`%s
%s
%s
export default defineConfig({
  base: "/_gox/client/",
  root: %s,
  publicDir: false,
  define: {
    "process.env.NODE_ENV": %s
  },
  plugins: %s,
  resolve: {
    alias: goxFrameworkResolveAliasEntries
  },
  css: {
    modules: {
      localsConvention: "camelCaseOnly",
      generateScopedName: goxCssModuleScopedName
    }
  },
  build: {
    outDir: %s,
    emptyOutDir: true,
    sourcemap: %t,
    minify: %t,
    lib: {
      entry: %s,
      formats: ["es"],
      name: %s,
      fileName: () => %s,
      cssFileName: %s
    },
    rollupOptions: {
      external: goxExternal,
      output: {
        manualChunks: goxManualChunks,
        chunkFileNames: goxChunkFileNames,
        assetFileNames: "assets/[hash][extname]"
      }
    }
  }
})
`, imports, vendorSetup, cssSetup, jsString(asset.EffectiveRoot()), jsDefineString(nodeEnvForMode(asset.Mode, asset.Dev)), plugins, jsString(asset.OutputAbsDir), profile.SourceMap, profile.Minify, jsString(asset.AbsPath), jsString(safeLibName(asset.Name)), jsString(asset.PublicBaseName()+".js"), jsString(asset.PublicBaseName()))

	return []byte(cfg), nil
}

func goxCssModuleConfigJS(componentName string) string {
	name := strings.TrimSpace(componentName)
	if name == "" {
		name = "Component"
	}
	return fmt.Sprintf(`const goxCssModuleComponentName = %s
function goxCssModuleFileName(filename) {
  const clean = String(filename || "").replaceAll("\\", "/")
  const base = clean.split("/").pop() || "style"
  return base.replace(/\.module\.[^.]+$/i, "").replace(/\.[^.]+$/i, "") || "style"
}
function goxCssModuleScopedName(local, filename) {
  return goxCssModuleComponentName + "." + goxCssModuleFileName(filename) + "." + local
}
`, jsString(name))
}

func goxVendorConfigJS(asset Asset, framework string, mode string, dev bool) string {
	prod := !dev && !strings.EqualFold(strings.TrimSpace(mode), ModeDev)
	packages := vendorPackages(framework)
	quoted := make([]string, 0, len(packages))
	for _, pkg := range packages {
		quoted = append(quoted, jsString(pkg))
	}
	resolveAliases := goxFrameworkResolveAliasJS(asset, framework, mode, dev)
	return fmt.Sprintf(`const goxVendorPackages = new Set([%s])
const goxVendorChunkName = %s
function goxRegExpEscape(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

const goxFrameworkResolveAliasEntries = [%s]

function goxPackageNameFromId(id) {
  if (typeof id !== "string") {
    return ""
  }
  const clean = id.replaceAll("\\", "/")
  const marker = "/node_modules/"
  const idx = clean.lastIndexOf(marker)
  if (idx < 0) {
    return ""
  }
  const rest = clean.slice(idx + marker.length)
  if (!rest || rest.startsWith(".")) {
    return ""
  }
  const parts = rest.split("/")
  if (parts[0] && parts[0].startsWith("@") && parts.length > 1) {
    return parts[0] + "/" + parts[1]
  }
  return parts[0] || ""
}

function goxManualChunks(id) {
  const pkg = goxPackageNameFromId(id)
  if (pkg && goxVendorPackages.has(pkg)) {
    return goxVendorChunkName
  }
}

function goxChunkFileNames(chunkInfo) {
  if (chunkInfo && chunkInfo.name === goxVendorChunkName) {
    return "vendor/[name]-[hash].js"
  }
  return "chunks/[hash].js"
}

const goxExternalClientModules = new Set([%s])
const goxExternalFrameworkModules = %t
const goxExternalPublicModules = new Set(["@gox/actions", "@gox/websocket"])

function goxExternal(id, parentId) {
  if (goxExternalPublicModules.has(id)) {
    return true
  }
  if (goxExternalFrameworkModules && goxExternalClientModules.has(id)) {
    return true
  }
  return false
}
`, strings.Join(quoted, ", "), jsString("vendor-"+strings.ToLower(strings.TrimSpace(framework))), resolveAliases, strings.Join(quotedFrameworkImportSpecifiers(framework), ", "), prod)
}

func goxFrameworkResolveAliasJS(asset Asset, framework string, mode string, dev bool) string {
	baseSpecs := quotedFrameworkResolveSpecifiers(framework)
	entries := make([]string, 0, len(baseSpecs)+2)
	prod := !dev && !strings.EqualFold(strings.TrimSpace(mode), ModeDev)
	framework = strings.ToLower(strings.TrimSpace(framework))
	modulePath := func(parts ...string) string {
		args := append([]string{assetFrameworkRoot(asset), "node_modules"}, parts...)
		return filepath.Join(args...)
	}
	if framework == "solid" && !prod {
		// GOX's generated Solid wrapper imports solid-js/web directly. Keep that
		// infrastructure import resolvable in dev, while leaving solid-js itself
		// to vite-plugin-solid so it can select the development runtime.
		entries = append(entries,
			`{ find: /^`+goxJSRegExpLiteral("solid-js/web")+`$/, replacement: `+jsString(modulePath("solid-js", "web", "dist", "web.js"))+` }`,
		)
	}
	if framework == "svelte" {
		entries = append(entries,
			`{ find: /^`+goxJSRegExpLiteral("svelte/internal/flags/tracing")+`$/, replacement: `+jsString(modulePath("svelte", "src", "internal", "flags", "tracing.js"))+` }`,
			`{ find: /^`+goxJSRegExpLiteral("svelte/internal/flags/async")+`$/, replacement: `+jsString(modulePath("svelte", "src", "internal", "flags", "async.js"))+` }`,
			`{ find: /^`+goxJSRegExpLiteral("svelte/internal/flags/legacy")+`$/, replacement: `+jsString(modulePath("svelte", "src", "internal", "flags", "legacy.js"))+` }`,
			`{ find: /^`+goxJSRegExpLiteral("svelte/internal/client")+`$/, replacement: `+jsString(modulePath("svelte", "src", "internal", "client", "index.js"))+` }`,
			`{ find: /^`+goxJSRegExpLiteral("svelte")+`$/, replacement: `+jsString(modulePath("svelte", "src", "index-client.js"))+` }`,
		)
	}
	for _, specifier := range baseSpecs {
		if framework == "svelte" && (specifier == "svelte" || specifier == "svelte/internal/client" || specifier == "svelte/internal/flags/legacy" || specifier == "svelte/internal/flags/async" || specifier == "svelte/internal/flags/tracing") {
			continue
		}
		// In development, vite-plugin-solid owns Solid's runtime resolution.
		// In particular it may select solid-js/dev; forcing require.resolve here
		// would bypass the plugin's development runtime selection.
		if framework == "solid" && !prod {
			// solid-js/web is resolved explicitly above for GOX's wrapper.
			// solid-js remains owned by vite-plugin-solid in development.
			continue
		}
		entries = append(entries, `{
  find: new RegExp("^" + goxRegExpEscape(`+jsString(specifier)+`) + "$"),
  replacement: require.resolve(`+jsString(specifier)+`)
}`)
	}
	return strings.Join(entries, ", ")
}

func goxJSRegExpLiteral(value string) string {
	return strings.ReplaceAll(regexpEscapeLite(value), "/", `\/`)
}

func regexpEscapeLite(value string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`.`, `\\.`,
		`+`, `\\+`,
		`*`, `\\*`,
		`?`, `\\?`,
		`^`, `\\^`,
		`$`, `\\$`,
		`(`, `\\(`,
		`)`, `\\)`,
		`[`, `\\[`,
		`]`, `\\]`,
		`{`, `\\{`,
		`}`, `\\}`,
		`|`, `\\|`,
	)
	return replacer.Replace(value)
}

func quotedFrameworkImportSpecifiers(framework string) []string {
	specifiers := FrameworkImportSpecifiers(framework)
	out := make([]string, 0, len(specifiers))
	for _, specifier := range specifiers {
		out = append(out, jsString(specifier))
	}
	return out
}

func quotedFrameworkResolveSpecifiers(framework string) []string {
	specifiers := FrameworkImportSpecifiers(framework)
	outSpecs := make([]string, 0, len(specifiers))
	for _, specifier := range specifiers {
		if strings.HasPrefix(specifier, "@gox/") {
			continue
		}
		outSpecs = append(outSpecs, specifier)
	}
	sort.SliceStable(outSpecs, func(i, j int) bool {
		return len(outSpecs[i]) > len(outSpecs[j])
	})
	return outSpecs
}

func GenerateViteFrameworkVendorConfig(asset Asset, outputAbsDir string, facades []frameworkModuleFacade, frameworkVersion string) ([]byte, error) {
	if len(facades) == 0 {
		return nil, fmt.Errorf("missing framework vendor facades for %q", asset.Framework)
	}
	packageJSONPath := filepath.Join(asset.EffectiveRoot(), "package.json")
	imports := `import { createRequire } from "node:module"` + "\n"
	imports += fmt.Sprintf("const require = createRequire(%s)\n", jsString(packageJSONPath))
	imports += `const { defineConfig } = await import(require.resolve("vite"))` + "\n"
	imports += `
function goxVendorManifestPlugin() {
  return {
    name: "gox-vendor-manifest",
    generateBundle(_options, bundle) {
      const files = Object.values(bundle)
        .map(item => item && item.fileName)
        .filter(Boolean)
        .sort()
      this.emitFile({
        type: "asset",
        fileName: ".gox-vendor-manifest.json",
        source: JSON.stringify({ version: 1, files }, null, 2) + "\n"
      })
    }
  }
}
`

	inputEntries := make([]string, 0, len(facades))
	for _, facade := range facades {
		name := strings.TrimSpace(facade.Name)
		if name == "" {
			return nil, fmt.Errorf("missing framework vendor facade for %q", asset.Framework)
		}
		inputEntries = append(inputEntries, jsString(name)+": "+jsString(frameworkModuleFacadePath(asset, name)))
	}
	sort.Strings(inputEntries)

	cfg := fmt.Sprintf(`%s
export default defineConfig({
  base: "/_gox/client/",
  root: %s,
  publicDir: false,
  define: {
    "process.env.NODE_ENV": %s
  },
  plugins: [goxVendorManifestPlugin()],
  build: {
    outDir: %s,
    emptyOutDir: false,
    sourcemap: %t,
    minify: %t,
    rollupOptions: {
      preserveEntrySignatures: "strict",
      input: {
        %s
      },
      output: {
        entryFileNames: "[name].%s.js",
        chunkFileNames: "chunks/%s/[hash].js",
        assetFileNames: "assets/[hash][extname]"
      }
    }
  }
})
`, imports, jsString(asset.EffectiveRoot()), jsDefineString(nodeEnvForMode(asset.Mode, asset.Dev)), jsString(outputAbsDir), asset.BuildProfile().SourceMap, asset.BuildProfile().Minify, strings.Join(inputEntries, ",\n        "), frameworkVersion, frameworkVersion)
	return []byte(cfg), nil
}

func goxClientGraphPluginJS(asset Asset) string {
	return fmt.Sprintf(`
function goxClientGraphPlugin() {
  const entry = %s
  const entryFile = %s
  const framework = %s
  const component = %s

  function add(set, value) {
    if (typeof value === "string" && value.length > 0 && !value.startsWith("\0")) {
      set.add(value)
    }
  }

  return {
    name: "gox-client-graph",
    generateBundle(_options, bundle) {
      const outputFiles = new Set()
      const moduleFiles = new Set()
      const imports = new Set()
      const dynamicImports = new Set()
      const nodes = []

      for (const item of Object.values(bundle)) {
        add(outputFiles, item.fileName)
        if (item.type !== "chunk") {
          continue
        }
        for (const id of item.moduleIds || []) {
          add(moduleFiles, id)
          const info = this.getModuleInfo(id)
          if (!info) {
            continue
          }
          const nodeImports = new Set()
          const nodeDynamicImports = new Set()
          for (const importedId of info.importedIds || []) {
            add(nodeImports, importedId)
            add(imports, importedId)
          }
          for (const importedId of info.dynamicallyImportedIds || []) {
            add(nodeDynamicImports, importedId)
            add(dynamicImports, importedId)
          }
          nodes.push({
            file: id,
            imports: Array.from(nodeImports).sort(),
            dynamicImports: Array.from(nodeDynamicImports).sort()
          })
        }
        for (const id of item.imports || []) {
          add(imports, id)
        }
        for (const id of item.dynamicImports || []) {
          add(dynamicImports, id)
        }
        if (item.viteMetadata) {
          for (const id of item.viteMetadata.importedCss || []) {
            add(imports, id)
          }
          for (const id of item.viteMetadata.importedAssets || []) {
            add(imports, id)
          }
        }
      }

      const graph = {
        entry,
        entryFile,
        framework,
        component,
        outputFiles: Array.from(outputFiles).sort(),
        moduleFiles: Array.from(moduleFiles).sort(),
        imports: Array.from(imports).sort(),
        dynamicImports: Array.from(dynamicImports).sort(),
        nodes: nodes.sort((a, b) => a.file.localeCompare(b.file))
      }
      console.log("__GOX_CLIENT_GRAPH_BEGIN__")
      console.log(JSON.stringify(graph))
      console.log("__GOX_CLIENT_GRAPH_END__")
    }
  }
}
`, jsString(asset.Path), jsString(asset.AbsPath), jsString(asset.Framework), jsString(asset.Name))
}

func assetViteEntry(asset Asset) string {
	if strings.TrimSpace(asset.WrapperAbsPath) != "" {
		return asset.WrapperAbsPath
	}
	return asset.AbsPath
}

func safeLibName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "GoxClient"
	}
	var b strings.Builder
	for i, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			continue
		}
		if i == 0 {
			b.WriteByte('G')
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "GoxClient"
	}
	return b.String()
}

func jsString(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, "\\", "/"))
}

func jsDefineString(s string) string {
	return strconv.Quote(strconv.Quote(s))
}

func nodeEnvForMode(mode string, dev bool) string {
	if dev || strings.EqualFold(strings.TrimSpace(mode), "dev") {
		return "development"
	}
	return "production"
}

func GenerateViteGroupConfig(assets []Asset, outputAbsDir string) ([]byte, error) {
	if len(assets) == 0 {
		return nil, fmt.Errorf("missing client assets for vite group config")
	}
	first := assets[0]
	framework := strings.ToLower(strings.TrimSpace(first.Framework))
	pluginSetup, pluginExprs, err := vitePlugin(framework)
	if err != nil {
		return nil, err
	}
	profile := first.BuildProfile()
	packageJSONPath := filepath.Join(first.EffectiveRoot(), "package.json")
	imports := `import { createRequire } from "node:module"` + "\n"
	imports += fmt.Sprintf("const require = createRequire(%s)\n", jsString(packageJSONPath))
	imports += `const { defineConfig } = await import(require.resolve("vite"))` + "\n"
	if pluginSetup != "" {
		imports += pluginSetup + "\n"
	}
	imports += goxClientGroupGraphPluginJS(assets) + "\n"

	plugins := "[goxClientGraphPlugin()]"
	if len(pluginExprs) > 0 {
		plugins = "[" + strings.Join(append(pluginExprs, "goxClientGraphPlugin()"), ", ") + "]"
	}
	vendorSetup := goxVendorConfigJS(first, framework, first.Mode, first.Dev)
	cssSetup := goxCssModuleConfigJS(first.Name)
	inputEntries := make([]string, 0, len(assets))
	for _, asset := range assets {
		inputEntries = append(inputEntries, fmt.Sprintf("    %s: %s", jsString(asset.PublicBaseName()), jsString(assetViteEntry(asset))))
	}

	componentDir := first.PublicComponentDirName()
	entryFileNames := componentDir + "/[name].js"
	chunkFileNames := componentDir + "/chunks/[hash].js"
	manualChunks := "undefined"
	if !first.Dev && !strings.EqualFold(strings.TrimSpace(first.Mode), ModeDev) {
		manualChunks = "goxManualChunks"
	}

	cfg := fmt.Sprintf(`%s
%s
%s
export default defineConfig({
  base: "/_gox/client/",
  root: %s,
  publicDir: false,
  define: {
    "process.env.NODE_ENV": %s
  },
  plugins: %s,
  resolve: {
    alias: goxFrameworkResolveAliasEntries
  },
  css: {
    modules: {
      localsConvention: "camelCaseOnly",
      generateScopedName: goxCssModuleScopedName
    }
  },
  build: {
    outDir: %s,
    emptyOutDir: false,
    sourcemap: %t,
    minify: %t,
    rollupOptions: {
      preserveEntrySignatures: "strict",
      external: goxExternal,
      input: {
%s
      },
      output: {
        manualChunks: %s,
        entryFileNames: %s,
        chunkFileNames: %s,
        assetFileNames: "assets/[hash][extname]"
      }
    }
  }
})
`, imports, vendorSetup, cssSetup, jsString(first.EffectiveRoot()), jsDefineString(nodeEnvForMode(first.Mode, first.Dev)), plugins, jsString(outputAbsDir), profile.SourceMap, profile.Minify, strings.Join(inputEntries, ",\n"), manualChunks, jsString(entryFileNames), jsString(chunkFileNames))
	return []byte(cfg), nil
}

func goxClientGroupGraphPluginJS(assets []Asset) string {
	entries := make([]string, 0, len(assets))
	for _, asset := range assets {
		entries = append(entries, fmt.Sprintf(`  {
    entry: %s,
    entryFile: %s,
    facadeFile: %s,
    framework: %s,
    component: %s
  }`, jsString(asset.Path), jsString(asset.AbsPath), jsString(assetViteEntry(asset)), jsString(asset.Framework), jsString(asset.Name)))
	}
	return fmt.Sprintf(`
function goxClientGraphPlugin() {
  const entries = [
%s
  ]

  function add(set, value) {
    if (typeof value === "string" && value.length > 0 && !value.startsWith("\0")) {
      set.add(value)
    }
  }

  return {
    name: "gox-client-graph",
    generateBundle(_options, bundle) {
      const bundleByFile = new Map()
      for (const item of Object.values(bundle)) {
        if (item && item.fileName) {
          bundleByFile.set(item.fileName, item)
        }
      }

      function collectChunkFiles(fileName, outputFiles, visited) {
        if (!fileName || visited.has(fileName)) return
        visited.add(fileName)
        const item = bundleByFile.get(fileName)
        if (!item) return
        add(outputFiles, item.fileName)
        if (item.viteMetadata) {
          for (const id of item.viteMetadata.importedCss || []) add(outputFiles, id)
          for (const id of item.viteMetadata.importedAssets || []) add(outputFiles, id)
        }
        for (const next of item.imports || []) collectChunkFiles(next, outputFiles, visited)
        for (const next of item.dynamicImports || []) collectChunkFiles(next, outputFiles, visited)
      }

      function collectModules(id, moduleFiles, nodes, visited) {
        if (!id || visited.has(id) || id.startsWith("\0")) return
        visited.add(id)
        add(moduleFiles, id)
        const info = this.getModuleInfo(id)
        if (!info) return
        const nodeImports = new Set()
        const nodeDynamicImports = new Set()
        for (const importedId of info.importedIds || []) {
          add(nodeImports, importedId)
          collectModules.call(this, importedId, moduleFiles, nodes, visited)
        }
        for (const importedId of info.dynamicallyImportedIds || []) {
          add(nodeDynamicImports, importedId)
          collectModules.call(this, importedId, moduleFiles, nodes, visited)
        }
        nodes.push({
          file: id,
          imports: Array.from(nodeImports).sort(),
          dynamicImports: Array.from(nodeDynamicImports).sort()
        })
      }

      for (const entry of entries) {
        const outputFiles = new Set()
        const moduleFiles = new Set()
        const nodes = []
        const imports = new Set()
        const dynamicImports = new Set()

        for (const item of Object.values(bundle)) {
          if (item && item.type === "chunk" && item.isEntry && item.facadeModuleId === entry.facadeFile) {
            collectChunkFiles(item.fileName, outputFiles, new Set())
          }
        }

        collectModules.call(this, entry.entryFile, moduleFiles, nodes, new Set())
        for (const node of nodes) {
          for (const id of node.imports || []) add(imports, id)
          for (const id of node.dynamicImports || []) add(dynamicImports, id)
        }

        const graph = {
          entry: entry.entry,
          entryFile: entry.entryFile,
          framework: entry.framework,
          component: entry.component,
          outputFiles: Array.from(outputFiles).sort(),
          moduleFiles: Array.from(moduleFiles).sort(),
          imports: Array.from(imports).sort(),
          dynamicImports: Array.from(dynamicImports).sort(),
          nodes: nodes.sort((a, b) => a.file.localeCompare(b.file))
        }
        console.log("__GOX_CLIENT_GRAPH_BEGIN__")
        console.log(JSON.stringify(graph))
        console.log("__GOX_CLIENT_GRAPH_END__")
      }
    }
  }
}
`, strings.Join(entries, ",\n"))
}
