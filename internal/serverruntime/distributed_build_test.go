package serverruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/project"

	"github.com/gox-lang/gox/server"
)

type buildCoordinatorTestRuntime struct {
	*clusterTestRuntime
	renewals atomic.Int32
}

type channelColdBuildLockProbeRuntime struct {
	*buildCoordinatorTestRuntime
	definition *channelDefinitionRuntime
	unlocked   atomic.Bool
}

func (runtime *channelColdBuildLockProbeRuntime) AcquireBuild(context.Context, server.ClusterBuildRequest) (server.ClusterBuildLease, error) {
	if runtime.definition.mu.TryLock() {
		runtime.unlocked.Store(true)
		runtime.definition.mu.Unlock()
	}
	return server.ClusterBuildLease{}, errors.New("stop after lock probe")
}

func (runtime *buildCoordinatorTestRuntime) AcquireBuild(context.Context, server.ClusterBuildRequest) (server.ClusterBuildLease, error) {
	return server.ClusterBuildLease{}, nil
}

func (runtime *buildCoordinatorTestRuntime) RenewBuild(_ context.Context, lease server.ClusterBuildLease, ttl time.Duration) (server.ClusterBuildLease, error) {
	runtime.renewals.Add(1)
	lease.ExpiresAt = time.Now().Add(ttl)
	return lease, nil
}

func (*buildCoordinatorTestRuntime) CompleteBuild(context.Context, server.ClusterBuildLease, server.ClusterBuildArtifact) error {
	return nil
}

func (*buildCoordinatorTestRuntime) FailBuild(context.Context, server.ClusterBuildLease, server.ClusterBuildFailure) error {
	return nil
}

func (*buildCoordinatorTestRuntime) WaitBuild(context.Context, server.ClusterBuildWaitRequest) (server.ClusterBuildResult, error) {
	return server.ClusterBuildResult{}, nil
}

func (*buildCoordinatorTestRuntime) InvalidateBuild(context.Context, server.ClusterBuildArtifact) error {
	return nil
}

func TestClusterBuildCoordinatorPreservesLocalFallbackAndRejectsPartialCluster(t *testing.T) {
	runtime := &Runtime{}
	coordinator, configured, err := runtime.clusterBuildCoordinator()
	if err != nil || configured || coordinator != nil {
		t.Fatalf("local coordinator = %#v, configured=%v, err=%v", coordinator, configured, err)
	}
	runtime.clusterRuntime = &clusterTestRuntime{}
	if _, configured, err := runtime.clusterBuildCoordinator(); !configured || err == nil {
		t.Fatalf("partial cluster configured=%v, err=%v", configured, err)
	}
	capable := &buildCoordinatorTestRuntime{clusterTestRuntime: &clusterTestRuntime{}}
	runtime.clusterRuntime = capable
	coordinator, configured, err = runtime.clusterBuildCoordinator()
	if err != nil || !configured || coordinator != capable {
		t.Fatalf("capable coordinator = %#v, configured=%v, err=%v", coordinator, configured, err)
	}
}

func TestDistributedBuildLeaseRenewalStopsCleanly(t *testing.T) {
	coordinator := &buildCoordinatorTestRuntime{clusterTestRuntime: &clusterTestRuntime{}}
	stop := startDistributedBuildLeaseRenewal(coordinator, server.ClusterBuildLease{
		Key: "/index", SourceVersion: strings.Repeat("a", 64), Token: "token", Acquired: true,
	}, 30*time.Millisecond)
	time.Sleep(25 * time.Millisecond)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if coordinator.renewals.Load() == 0 {
		t.Fatal("long-running build lease was not renewed")
	}
}

func TestColdChannelBuildDoesNotHoldDefinitionLockDuringCoordination(t *testing.T) {
	root := t.TempDir()
	entryPath := filepath.Join(root, "websocket", "chat", "chat.gox")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("module chat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	definition := &channelDefinitionRuntime{entry: project.ChannelEntry{Name: "Chat", File: "websocket/chat/chat.gox"}}
	definition.route = &GoxRouteRuntime{
		RoutePath: channelRoutePrefix + "Chat", AbsPath: entryPath, RelPath: "websocket/chat/chat.gox",
		ModTime: info.ModTime(), Size: info.Size(), BuildDir: filepath.Join(root, ".build", "websocket", "channels", "Chat"),
		BuildVersion: "cold", GeneratedPath: filepath.Join(root, ".build", "cold.go"),
		PluginPath: filepath.Join(root, ".build", "cold.so"), GoBinary: "go", State: GoxRouteCold,
	}
	coordinator := &channelColdBuildLockProbeRuntime{
		buildCoordinatorTestRuntime: &buildCoordinatorTestRuntime{clusterTestRuntime: &clusterTestRuntime{}},
		definition:                  definition,
	}
	runtime := &Runtime{ProjectRoot: root, clusterRuntime: coordinator}
	if _, _, err := runtime.ensureChannelDefinition(definition); err == nil {
		t.Fatal("lock probe unexpectedly completed a build")
	}
	if !coordinator.unlocked.Load() {
		t.Fatal("cold build coordination ran while the Channel definition lock was held")
	}
}

func TestFindGoxRouteByEntryIncludesLoadedChannelDefinitions(t *testing.T) {
	root := t.TempDir()
	entryPath := filepath.Join(root, "websocket", "chat", "chat.gox")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("module chat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	route := &GoxRouteRuntime{RoutePath: channelRoutePrefix + "Chat", AbsPath: entryPath}
	runtime := &Runtime{channelDefinitions: map[string]*channelDefinitionRuntime{
		"Chat": {route: route},
	}}
	found := runtime.findGoxRouteByEntry(hotreload.ReloadEntry{EntryAbsPath: entryPath})
	if found != route {
		t.Fatalf("found route = %#v", found)
	}
}

func TestResolveSharedBuildPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveSharedBuildPath(root, "../outside.so"); err == nil {
		t.Fatal("shared artifact traversal was accepted")
	}
	want := filepath.Join(root, ".build", "pages", "index", "version.so")
	got, err := resolveSharedBuildPath(root, ".build/pages/index/version.so")
	if err != nil || got != want {
		t.Fatalf("resolved path = %q, err=%v", got, err)
	}
}

func TestDistributedBuildManifestRoundTripsNewDependencyGraph(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "websocket", "chat", "chat.gox")
	dependency := filepath.Join(root, "websocket", "chat", ".validate.gox")
	buildDir := filepath.Join(root, ".build", "websocket", "channels", "Chat")
	for _, path := range []string{entry, dependency} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{ProjectRoot: root}
	compileResult := &goxcompiler.CompileResult{
		EntryPath: "websocket/chat/chat.gox", EntryAbsPath: entry,
		Dependencies: []goxcompiler.CompileDependency{
			{Path: "websocket/chat/chat.gox", AbsPath: entry, IsEntry: true},
			{Path: "websocket/chat/.validate.gox", AbsPath: dependency},
		},
	}
	_, buildVersion, err := goxDependencyBuildVersion(goxRouteDependenciesFromCompileResult(compileResult))
	if err != nil {
		t.Fatal(err)
	}
	candidate := &GoxRouteRuntime{
		RoutePath: channelRoutePrefix + "Chat", BuildDir: buildDir,
		BuildVersion: buildVersion, CompileResult: compileResult,
	}
	sourceVersion := strings.Repeat("a", 64)
	manifestPath, info, digest, err := runtime.writeGoxBuildManifest(candidate, sourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	artifact := server.ClusterBuildArtifact{
		Key: candidate.RoutePath, SourceVersion: sourceVersion, BuildVersion: candidate.BuildVersion,
		ManifestRelativePath: filepath.ToSlash(strings.TrimPrefix(manifestPath, root+string(filepath.Separator))),
		ManifestSize:         info.Size(), ManifestSHA256: digest,
	}
	result, err := runtime.readGoxBuildManifest(manifestPath, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dependencies) != 2 || result.Dependencies[1].AbsPath != dependency {
		t.Fatalf("manifest dependencies = %#v", result.Dependencies)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), root) {
		t.Fatalf("manifest leaked project root: %s", raw)
	}
}

func TestDistributedBuildManifestRejectsIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.json")
	data := `{"schemaVersion":1,"key":"/other","sourceVersion":"source","buildVersion":"build","compileResult":{"Dependencies":[{"AbsPath":"@project/pages/index.gox"}]}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{ProjectRoot: root}
	_, err := runtime.readGoxBuildManifest(path, server.ClusterBuildArtifact{Key: "/index", SourceVersion: "source", BuildVersion: "build"})
	if err == nil {
		t.Fatal("mismatched manifest identity was accepted")
	}
}

func TestDistributedBuildRejectsCorruptedManifestBeforePluginLoad(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, ".build", "pages", "index")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	generatedPath := filepath.Join(buildDir, "build.go")
	pluginPath := filepath.Join(buildDir, "build.so")
	manifestPath := filepath.Join(buildDir, "build.manifest.json")
	for path, data := range map[string][]byte{
		generatedPath: []byte("package main"), pluginPath: []byte("not-a-plugin"), manifestPath: []byte(`{"schemaVersion":1}`),
	} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pluginInfo, pluginDigest, err := hashGoxBuildFile(pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestInfo, manifestDigest, err := hashGoxBuildFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{ProjectRoot: root}
	route := &GoxRouteRuntime{RoutePath: "/index", AbsPath: filepath.Join(root, "pages", "index.gox")}
	_, err = runtime.loadSharedGoxCandidate(route, server.ClusterBuildArtifact{
		Key: "/index", SourceVersion: "source", BuildVersion: "build",
		GeneratedRelativePath: ".build/pages/index/build.go",
		PluginRelativePath:    ".build/pages/index/build.so", PluginSize: pluginInfo.Size(), PluginSHA256: pluginDigest,
		ManifestRelativePath: ".build/pages/index/build.manifest.json", ManifestSize: manifestInfo.Size(), ManifestSHA256: manifestDigest,
	})
	if err == nil || !strings.Contains(err.Error(), "manifest checksum mismatch") {
		t.Fatalf("corrupted manifest error = %v", err)
	}
}
