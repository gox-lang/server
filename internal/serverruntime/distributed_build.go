package serverruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const (
	distributedBuildLeaseTTL = 30 * time.Second
	distributedBuildWaitTTL  = 2 * time.Minute
	distributedBuildGCGrace  = 15 * time.Minute
)

func (r *Runtime) rebuildGoxRouteTransactional(route *GoxRouteRuntime) error {
	if route == nil {
		return errors.New("gox rebuild route is unavailable")
	}
	coordinator, configured, err := r.clusterBuildCoordinator()
	if err != nil {
		return err
	}
	if !configured {
		candidate := cloneGoxRouteForRebuild(route)
		if err := r.buildAndLoadGoxCandidate(candidate); err != nil {
			return err
		}
		return r.commitGoxRouteCandidate(route, candidate)
	}

	sourceVersion, err := currentGoxRouteSourceVersion(route)
	if err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to fingerprint hot reload source", err)
	}
	request := server.ClusterBuildRequest{
		Key: route.RoutePath, SourceVersion: sourceVersion, LeaseTTL: distributedBuildLeaseTTL,
	}
	for attempts := 0; attempts < 3; attempts++ {
		lease, err := coordinator.AcquireBuild(context.Background(), request)
		if err != nil {
			return newGoxError(gox.GoxErrorBuild, route, nil, "distributed build lease failed", err)
		}
		if lease.Acquired {
			return r.buildOwnedGoxRoute(route, coordinator, lease)
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), distributedBuildWaitTTL)
		result, waitErr := coordinator.WaitBuild(waitCtx, server.ClusterBuildWaitRequest{
			Key: request.Key, SourceVersion: request.SourceVersion,
		})
		cancel()
		if waitErr != nil {
			if attempts < 2 {
				continue
			}
			return newGoxError(gox.GoxErrorBuild, route, nil, "distributed build wait failed", waitErr)
		}
		switch result.State {
		case server.ClusterBuildResultCompleted:
			candidate, err := r.loadSharedGoxCandidate(route, result.Artifact)
			if err != nil {
				// A durable coordinator result may outlive files removed manually or
				// by an older runtime. Retire only this exact completed artifact and
				// let the next attempt elect a fresh compiler.
				if invalidateErr := coordinator.InvalidateBuild(context.Background(), result.Artifact); invalidateErr != nil {
					return newGoxError(gox.GoxErrorBuild, route, nil, "failed to invalidate unavailable distributed artifact", invalidateErr)
				}
				if attempts < 2 {
					continue
				}
				return err
			}
			return r.commitGoxRouteCandidate(route, candidate)
		case server.ClusterBuildResultFailed:
			return newGoxError(gox.GoxErrorBuild, route, nil, "distributed compiler failed", errors.New(result.Failure.Code+": "+result.Failure.Message))
		default:
			return newGoxError(gox.GoxErrorBuild, route, nil, "distributed build returned an invalid state", nil)
		}
	}
	return newGoxError(gox.GoxErrorBuild, route, nil, "distributed build attempts exhausted", nil)
}

func (r *Runtime) clusterBuildCoordinator() (server.ClusterBuildCoordinator, bool, error) {
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	r.sessionStorageMu.RUnlock()
	if cluster == nil {
		return nil, false, nil
	}
	coordinator, ok := cluster.(server.ClusterBuildCoordinator)
	if !ok {
		return nil, true, errors.New("configured cluster does not implement distributed build coordination")
	}
	return coordinator, true, nil
}

func (r *Runtime) buildOwnedGoxRoute(route *GoxRouteRuntime, coordinator server.ClusterBuildCoordinator, lease server.ClusterBuildLease) error {
	stopRenewal := startDistributedBuildLeaseRenewal(coordinator, lease, distributedBuildLeaseTTL)
	candidate := cloneGoxRouteForRebuild(route)
	if err := r.buildAndLoadGoxCandidate(candidate); err != nil {
		_ = stopRenewal()
		_ = coordinator.FailBuild(context.Background(), lease, server.ClusterBuildFailure{
			Key: lease.Key, SourceVersion: lease.SourceVersion,
			Code: "build-failed", Message: "GOX plugin compilation failed",
		})
		return err
	}
	artifact, err := r.goxClusterBuildArtifact(candidate, lease.SourceVersion)
	if err != nil {
		_ = stopRenewal()
		_ = coordinator.FailBuild(context.Background(), lease, server.ClusterBuildFailure{
			Key: lease.Key, SourceVersion: lease.SourceVersion,
			Code: "artifact-invalid", Message: "compiled plugin artifact validation failed",
		})
		return err
	}
	if err := stopRenewal(); err != nil {
		return newGoxError(gox.GoxErrorBuild, route, nil, "distributed build lease renewal failed", err)
	}
	if err := coordinator.CompleteBuild(context.Background(), lease, artifact); err != nil {
		return newGoxError(gox.GoxErrorBuild, route, nil, "distributed build completion failed", err)
	}
	return r.commitGoxRouteCandidate(route, candidate)
}

func startDistributedBuildLeaseRenewal(coordinator server.ClusterBuildCoordinator, lease server.ClusterBuildLease, ttl time.Duration) func() error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				renewed, err := coordinator.RenewBuild(ctx, lease, ttl)
				if err != nil {
					if ctx.Err() != nil {
						done <- nil
					} else {
						done <- err
					}
					return
				}
				lease = renewed
			}
		}
	}()
	return func() error {
		cancel()
		return <-done
	}
}

func cloneGoxRouteForRebuild(route *GoxRouteRuntime) *GoxRouteRuntime {
	route.mu.Lock()
	candidate := &GoxRouteRuntime{
		RoutePath: route.RoutePath, AbsPath: route.AbsPath, RelPath: route.RelPath,
		ModTime: route.ModTime, Size: route.Size, BuildDir: route.BuildDir,
		BuildVersion: route.BuildVersion, GeneratedPath: route.GeneratedPath,
		PluginPath: route.PluginPath, GoBinary: route.GoBinary, State: GoxRouteCold,
		Dependencies:  append([]GoxRouteDependency(nil), route.Dependencies...),
		CompileResult: cloneGoxCompileResult(route.CompileResult),
	}
	route.mu.Unlock()
	resetGoxRouteBuildLocked(candidate)
	return candidate
}

func currentGoxRouteSourceVersion(route *GoxRouteRuntime) (string, error) {
	route.mu.Lock()
	deps := append([]GoxRouteDependency(nil), route.Dependencies...)
	if len(deps) == 0 {
		deps = []GoxRouteDependency{{AbsPath: route.AbsPath, Size: route.Size, ModTime: route.ModTime, IsEntry: true}}
	}
	route.mu.Unlock()
	_, version, err := goxDependencyBuildVersion(deps)
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", errors.New("source fingerprint is empty")
	}
	return version, nil
}

func (r *Runtime) buildAndLoadGoxCandidate(candidate *GoxRouteRuntime) error {
	if err := r.compileGoxRouteLocked(candidate); err != nil {
		return err
	}
	if err := r.buildGoxRoutePluginLocked(candidate); err != nil {
		return err
	}
	if err := r.loadGoxRoutePluginLocked(candidate); err != nil {
		return err
	}
	candidate.State = GoxRouteLoaded
	candidate.LastError = ""
	return nil
}

func (r *Runtime) goxClusterBuildArtifact(candidate *GoxRouteRuntime, sourceVersion string) (server.ClusterBuildArtifact, error) {
	projectRoot := r.projectRoot()
	generatedRelative, err := filepath.Rel(projectRoot, candidate.GeneratedPath)
	if err != nil || !isPathInside(candidate.GeneratedPath, projectRoot) {
		return server.ClusterBuildArtifact{}, newGoxError(gox.GoxErrorBuild, candidate, nil, "generated artifact is outside project root", err)
	}
	pluginRelative, err := filepath.Rel(projectRoot, candidate.PluginPath)
	if err != nil || !isPathInside(candidate.PluginPath, projectRoot) {
		return server.ClusterBuildArtifact{}, newGoxError(gox.GoxErrorBuild, candidate, nil, "plugin artifact is outside project root", err)
	}
	info, digest, err := hashGoxBuildFile(candidate.PluginPath)
	if err != nil {
		return server.ClusterBuildArtifact{}, newGoxError(gox.GoxErrorBuild, candidate, nil, "failed to hash plugin artifact", err)
	}
	manifestPath, manifestInfo, manifestDigest, err := r.writeGoxBuildManifest(candidate, sourceVersion)
	if err != nil {
		return server.ClusterBuildArtifact{}, newGoxError(gox.GoxErrorBuild, candidate, nil, "failed to write build manifest", err)
	}
	manifestRelative, err := filepath.Rel(projectRoot, manifestPath)
	if err != nil || !isPathInside(manifestPath, projectRoot) {
		return server.ClusterBuildArtifact{}, newGoxError(gox.GoxErrorBuild, candidate, nil, "manifest artifact is outside project root", err)
	}
	return server.ClusterBuildArtifact{
		Key: candidate.RoutePath, SourceVersion: sourceVersion, BuildVersion: candidate.BuildVersion,
		GeneratedRelativePath: filepath.ToSlash(generatedRelative),
		PluginRelativePath:    filepath.ToSlash(pluginRelative),
		PluginSize:            info.Size(), PluginSHA256: digest,
		ManifestRelativePath: filepath.ToSlash(manifestRelative),
		ManifestSize:         manifestInfo.Size(), ManifestSHA256: manifestDigest,
	}, nil
}

func (r *Runtime) loadSharedGoxCandidate(route *GoxRouteRuntime, artifact server.ClusterBuildArtifact) (*GoxRouteRuntime, error) {
	if artifact.Key != route.RoutePath || artifact.BuildVersion == "" {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed artifact identity is invalid", nil)
	}
	projectRoot := r.projectRoot()
	generatedPath, err := resolveSharedBuildPath(projectRoot, artifact.GeneratedRelativePath)
	if err != nil {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed generated path is invalid", err)
	}
	pluginPath, err := resolveSharedBuildPath(projectRoot, artifact.PluginRelativePath)
	if err != nil {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed plugin path is invalid", err)
	}
	if info, err := os.Stat(generatedPath); err != nil || info.IsDir() {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed generated artifact is unavailable", err)
	}
	pluginInfo, digest, err := hashGoxBuildFile(pluginPath)
	if err != nil || pluginInfo.Size() != artifact.PluginSize || digest != strings.ToLower(artifact.PluginSHA256) {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed plugin checksum mismatch", err)
	}
	manifestPath, err := resolveSharedBuildPath(projectRoot, artifact.ManifestRelativePath)
	if err != nil {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed manifest path is invalid", err)
	}
	manifestInfo, manifestDigest, err := hashGoxBuildFile(manifestPath)
	if err != nil || manifestInfo.Size() != artifact.ManifestSize || manifestDigest != strings.ToLower(artifact.ManifestSHA256) {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed manifest checksum mismatch", err)
	}
	compileResult, err := r.readGoxBuildManifest(manifestPath, artifact)
	if err != nil {
		return nil, newGoxError(gox.GoxErrorLoad, route, nil, "distributed manifest is invalid", err)
	}
	candidate := cloneGoxRouteForRebuild(route)
	candidate.BuildDir = filepath.Dir(pluginPath)
	candidate.BuildVersion = artifact.BuildVersion
	candidate.GeneratedPath = generatedPath
	candidate.PluginPath = pluginPath
	candidate.GeneratedExists = true
	candidate.PluginExists = true
	candidate.PluginSize = pluginInfo.Size()
	candidate.CompileResult = compileResult
	candidate.Dependencies = goxRouteDependenciesFromCompileResult(compileResult)
	if err := r.loadGoxRoutePluginLocked(candidate); err != nil {
		return nil, err
	}
	candidate.State = GoxRouteLoaded
	return candidate, nil
}

func resolveSharedBuildPath(projectRoot, relative string) (string, error) {
	if strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) {
		return "", errors.New("artifact path must be relative")
	}
	resolved := filepath.Clean(filepath.Join(projectRoot, filepath.FromSlash(relative)))
	if !isPathInside(resolved, projectRoot) {
		return "", errors.New("artifact path escapes project root")
	}
	return resolved, nil
}

func hashGoxBuildFile(path string) (os.FileInfo, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if info.IsDir() {
		return nil, "", errors.New("build artifact is a directory")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, "", err
	}
	return info, hex.EncodeToString(hash.Sum(nil)), nil
}

func refreshedGoxDependencies(route *GoxRouteRuntime) []GoxRouteDependency {
	route.mu.Lock()
	deps := append([]GoxRouteDependency(nil), route.Dependencies...)
	route.mu.Unlock()
	for index := range deps {
		if info, err := os.Stat(deps[index].AbsPath); err == nil && !info.IsDir() {
			deps[index].Size = info.Size()
			deps[index].ModTime = info.ModTime()
		}
	}
	return deps
}

func (r *Runtime) commitGoxRouteCandidate(route, candidate *GoxRouteRuntime) error {
	if route.RoutePath == webSocketModuleRoutePath && candidate.WebSocketHandler == nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "compiled WebSocket handler is unavailable", nil)
	}
	var prepared *preparedChannelReload
	var err error
	if isChannelRoutePath(route.RoutePath) {
		if candidate.ChannelHandler == nil {
			return newGoxError(gox.GoxErrorLoad, route, nil, "compiled Channel handler is unavailable", nil)
		}
		name := strings.TrimPrefix(route.RoutePath, channelRoutePrefix)
		prepared, err = r.prepareChannelReload(name, candidate.BuildVersion, candidate.ChannelHandler)
		if err != nil {
			return newGoxError(gox.GoxErrorLoad, route, nil, "failed to prepare Channel reload", err)
		}
		defer func() {
			if prepared != nil {
				prepared.Abort()
			}
		}()
		if err := prepared.Commit(); err != nil {
			return newGoxError(gox.GoxErrorLoad, route, nil, "failed to commit Channel reload", err)
		}
		prepared = nil
	}
	route.mu.Lock()
	var reconnect []*gox.Socket
	if route.RoutePath == webSocketModuleRoutePath && route.WebSocketHandler != nil && route.BuildVersion != candidate.BuildVersion {
		// Capture only connections using the previous module. Handshakes still
		// in progress check their captured version after joining the registry.
		reconnect = r.webSocketRegistry().all()
	}
	copyGoxRouteBuildState(route, candidate)
	route.mu.Unlock()
	if candidate.CompileResult != nil {
		r.registerHotReloadCompileResult(hotReloadKindForGoxRoute(route.RoutePath), candidate.CompileResult)
		r.registerClientAssets(candidate.CompileResult)
	}
	for _, socket := range reconnect {
		socket.Disconnect(gox.WSCloseReconnect)
	}
	if err := r.cleanupGoxRouteBuildArtifacts(route); err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "failed to clean stale gox build artifacts", err)
	}
	return nil
}

func (r *Runtime) cleanupGoxRouteBuildArtifacts(route *GoxRouteRuntime) error {
	r.sessionStorageMu.RLock()
	distributed := r.clusterRuntime != nil
	r.sessionStorageMu.RUnlock()
	if distributed {
		// The coordinator keeps completed results for five minutes. A longer
		// filesystem grace window lets every waiter validate and open the same
		// immutable artifacts before an owner removes older generations.
		return cleanupGoxRouteBuildArtifactsWithGrace(r.projectRoot(), route, distributedBuildGCGrace)
	}
	return cleanupGoxRouteBuildArtifacts(r.projectRoot(), route)
}

func copyGoxRouteBuildState(target, source *GoxRouteRuntime) {
	target.ModTime = source.ModTime
	target.Size = source.Size
	target.BuildDir = source.BuildDir
	target.BuildVersion = source.BuildVersion
	target.GeneratedPath = source.GeneratedPath
	target.PluginPath = source.PluginPath
	target.State = source.State
	target.LastError = source.LastError
	target.GeneratedExists = source.GeneratedExists
	target.GeneratedSize = source.GeneratedSize
	target.PluginExists = source.PluginExists
	target.PluginSize = source.PluginSize
	target.Plugin = source.Plugin
	target.HTTPHandlers = source.HTTPHandlers
	target.HTTPContentTypes = source.HTTPContentTypes
	target.Actions = source.Actions
	target.WebSocketHandler = source.WebSocketHandler
	target.ChannelHandler = source.ChannelHandler
	target.Dependencies = append([]GoxRouteDependency(nil), source.Dependencies...)
	target.CompileResult = cloneGoxCompileResult(source.CompileResult)
}

const (
	goxBuildManifestSchema       = 1
	goxManifestProjectPathPrefix = "@project/"
)

type goxBuildManifest struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Key           string                     `json:"key"`
	SourceVersion string                     `json:"sourceVersion"`
	BuildVersion  string                     `json:"buildVersion"`
	CompileResult *goxcompiler.CompileResult `json:"compileResult"`
}

func (r *Runtime) writeGoxBuildManifest(candidate *GoxRouteRuntime, sourceVersion string) (string, os.FileInfo, string, error) {
	if candidate == nil || candidate.CompileResult == nil {
		return "", nil, "", errors.New("compile result is unavailable")
	}
	result := cloneGoxCompileResult(candidate.CompileResult)
	if result == nil {
		return "", nil, "", errors.New("compile result cannot be cloned")
	}
	rebaseGoxCompileResultPaths(result, r.projectRoot(), true)
	data, err := json.Marshal(goxBuildManifest{
		SchemaVersion: goxBuildManifestSchema,
		Key:           candidate.RoutePath, SourceVersion: sourceVersion,
		BuildVersion: candidate.BuildVersion, CompileResult: result,
	})
	if err != nil {
		return "", nil, "", err
	}
	path := filepath.Join(candidate.BuildDir, candidate.BuildVersion+".manifest.json")
	temporary, err := os.CreateTemp(candidate.BuildDir, ".manifest-*.tmp")
	if err != nil {
		return "", nil, "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err = temporary.Write(data); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", nil, "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", nil, "", err
	}
	info, digest, err := hashGoxBuildFile(path)
	return path, info, digest, err
}

func (r *Runtime) readGoxBuildManifest(path string, artifact server.ClusterBuildArtifact) (*goxcompiler.CompileResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest goxBuildManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != goxBuildManifestSchema || manifest.Key != artifact.Key ||
		manifest.SourceVersion != artifact.SourceVersion || manifest.BuildVersion != artifact.BuildVersion ||
		manifest.CompileResult == nil {
		return nil, errors.New("manifest identity does not match artifact")
	}
	rebaseGoxCompileResultPaths(manifest.CompileResult, r.projectRoot(), false)
	if len(manifest.CompileResult.Dependencies) == 0 {
		return nil, errors.New("manifest contains no dependencies")
	}
	dependencies := goxRouteDependenciesFromCompileResult(manifest.CompileResult)
	_, observedBuildVersion, err := goxDependencyBuildVersion(dependencies)
	if err != nil {
		return nil, err
	}
	if observedBuildVersion != manifest.BuildVersion {
		return nil, errors.New("manifest dependencies no longer match build version")
	}
	return manifest.CompileResult, nil
}

func cloneGoxCompileResult(result *goxcompiler.CompileResult) *goxcompiler.CompileResult {
	if result == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	var clone goxcompiler.CompileResult
	if json.Unmarshal(data, &clone) != nil {
		return nil
	}
	return &clone
}

func rebaseGoxCompileResultPaths(result *goxcompiler.CompileResult, projectRoot string, encode bool) {
	if result == nil {
		return
	}
	pathValue := func(value string) string { return rebaseGoxManifestPath(value, projectRoot, encode) }
	result.EntryAbsPath = pathValue(result.EntryAbsPath)
	result.OutAbsPath = pathValue(result.OutAbsPath)
	result.SourceMapPath = pathValue(result.SourceMapPath)
	for index := range result.Dependencies {
		result.Dependencies[index].AbsPath = pathValue(result.Dependencies[index].AbsPath)
	}
	for index := range result.ResolutionDependencies {
		result.ResolutionDependencies[index] = pathValue(result.ResolutionDependencies[index])
	}
	for index := range result.Modules {
		result.Modules[index].AbsPath = pathValue(result.Modules[index].AbsPath)
	}
	for index := range result.ClientComponents {
		result.ClientComponents[index].AbsPath = pathValue(result.ClientComponents[index].AbsPath)
		result.ClientComponents[index].ModuleAbsPath = pathValue(result.ClientComponents[index].ModuleAbsPath)
		result.ClientComponents[index].OutputDir = pathValue(result.ClientComponents[index].OutputDir)
	}
	for url, file := range result.ClientAssetMap {
		result.ClientAssetMap[url] = pathValue(file)
	}
	for index := range result.ClientBundles {
		bundle := &result.ClientBundles[index]
		bundle.EntryFile = pathValue(bundle.EntryFile)
		for assetIndex := range bundle.Assets {
			bundle.Assets[assetIndex].File = pathValue(bundle.Assets[assetIndex].File)
		}
		bundle.Graph.EntryFile = pathValue(bundle.Graph.EntryFile)
		for fileIndex := range bundle.Graph.OutputFiles {
			bundle.Graph.OutputFiles[fileIndex] = pathValue(bundle.Graph.OutputFiles[fileIndex])
		}
		for fileIndex := range bundle.Graph.ModuleFiles {
			bundle.Graph.ModuleFiles[fileIndex] = pathValue(bundle.Graph.ModuleFiles[fileIndex])
		}
		for nodeIndex := range bundle.Graph.Nodes {
			bundle.Graph.Nodes[nodeIndex].File = pathValue(bundle.Graph.Nodes[nodeIndex].File)
		}
	}
}

func rebaseGoxManifestPath(value, projectRoot string, encode bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if encode {
		if !filepath.IsAbs(value) || !isPathInside(value, projectRoot) {
			return value
		}
		relative, err := filepath.Rel(projectRoot, value)
		if err != nil {
			return value
		}
		return goxManifestProjectPathPrefix + filepath.ToSlash(relative)
	}
	if !strings.HasPrefix(value, goxManifestProjectPathPrefix) {
		return value
	}
	relative := strings.TrimPrefix(value, goxManifestProjectPathPrefix)
	resolved, err := resolveSharedBuildPath(projectRoot, relative)
	if err != nil {
		return value
	}
	return resolved
}

func hotReloadKindForGoxRoute(routePath string) hotreload.ReloadKind {
	if routePath == goxErrorRoutePath {
		return hotreload.ReloadError
	}
	return hotreload.ReloadPage
}
