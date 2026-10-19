package toolchain

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gox-lang/server/internal/logs"
)

type NodeInfo struct{ ExactVersion string }
type PNPMInfo struct{ ExactVersion string }

var frontendToolchainCacheMu sync.Mutex
var frontendNodeCache = map[string]NodePaths{}
var frontendPNPMCache = map[string]PNPMPaths{}

func NewNode(version string) NodeInfo {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "node")
	version = strings.TrimPrefix(version, "v")
	return NodeInfo{ExactVersion: "node" + version}
}

func NewPNPM(version string) PNPMInfo {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "pnpm")
	version = strings.TrimPrefix(version, "v")
	return PNPMInfo{ExactVersion: "pnpm" + version}
}

type NodePaths struct {
	HomeDir      string
	ToolchainDir string
	VersionDir   string
	NodeRoot     string
	NodeBin      string
	NodeExec     string
	NPMExec      string
	NPXExec      string
	CorepackExec string
	ArchivePath  string
}

type PNPMPaths struct {
	HomeDir      string
	ToolchainDir string
	VersionDir   string
	PNPMRoot     string
	PNPMBin      string
	PNPMExec     string
}

func ResolveNodePaths(info NodeInfo, platform Platform) (NodePaths, error) {
	if platform.OS == "windows" {
		return NodePaths{}, fmt.Errorf("managed frontend toolchains on Windows require WSL")
	}
	home, err := ResolveGoxHome()
	if err != nil {
		return NodePaths{}, err
	}
	repo := filepath.Join(home, ".toolchain")
	versionDir := filepath.Join(repo, info.ExactVersion)
	root := filepath.Join(versionDir, nodeArchiveRoot(info, platform))
	bin := filepath.Join(root, "bin")
	return NodePaths{HomeDir: home, ToolchainDir: repo, VersionDir: versionDir, NodeRoot: root, NodeBin: bin, NodeExec: filepath.Join(bin, "node"), NPMExec: filepath.Join(bin, "npm"), NPXExec: filepath.Join(bin, "npx"), CorepackExec: filepath.Join(bin, "corepack"), ArchivePath: filepath.Join(versionDir, nodeArchiveName(info, platform))}, nil
}

func ResolvePNPMPaths(info PNPMInfo) (PNPMPaths, error) {
	home, err := ResolveGoxHome()
	if err != nil {
		return PNPMPaths{}, err
	}
	repo := filepath.Join(home, ".toolchain")
	versionDir := filepath.Join(repo, info.ExactVersion)
	root := filepath.Join(versionDir, "pnpm")
	bin := filepath.Join(root, "bin")
	return PNPMPaths{HomeDir: home, ToolchainDir: repo, VersionDir: versionDir, PNPMRoot: root, PNPMBin: bin, PNPMExec: filepath.Join(bin, "pnpm")}, nil
}

func EnsureNode(info NodeInfo) (NodePaths, error) {
	frontendToolchainCacheMu.Lock()
	if paths, ok := frontendNodeCache[info.ExactVersion]; ok && fileExists(paths.NodeExec) {
		frontendToolchainCacheMu.Unlock()
		return paths, nil
	}
	frontendToolchainCacheMu.Unlock()

	platform, err := CurrentPlatform()
	if err != nil {
		return NodePaths{}, err
	}
	paths, err := ResolveNodePaths(info, platform)
	if err != nil {
		return NodePaths{}, err
	}
	logs.Info("checking Node toolchain: %s", info.ExactVersion)
	if fileExists(paths.NodeExec) {
		logs.Info("Node toolchain ready: %s", paths.NodeExec)
		frontendToolchainCacheMu.Lock()
		frontendNodeCache[info.ExactVersion] = paths
		frontendToolchainCacheMu.Unlock()
		return paths, nil
	}
	if err := os.MkdirAll(paths.VersionDir, 0755); err != nil {
		return NodePaths{}, err
	}
	url := nodeDownloadURL(info, platform)
	if !fileExists(paths.ArchivePath) {
		logs.Info("downloading Node toolchain: %s", url)
		if err := Download(url, paths.ArchivePath); err != nil {
			return NodePaths{}, err
		}
	}
	logs.Info("extracting Node toolchain to: %s", paths.VersionDir)
	if err := ExtractTarXz(paths.ArchivePath, paths.VersionDir); err != nil {
		return NodePaths{}, err
	}
	if !fileExists(paths.NodeExec) {
		return NodePaths{}, fmt.Errorf("node toolchain extraction failed: missing %s", paths.NodeExec)
	}
	frontendToolchainCacheMu.Lock()
	frontendNodeCache[info.ExactVersion] = paths
	frontendToolchainCacheMu.Unlock()
	return paths, nil
}

func EnsurePNPM(info PNPMInfo, node NodePaths) (PNPMPaths, error) {
	frontendToolchainCacheMu.Lock()
	if paths, ok := frontendPNPMCache[info.ExactVersion]; ok && fileExists(paths.PNPMExec) {
		frontendToolchainCacheMu.Unlock()
		return paths, nil
	}
	frontendToolchainCacheMu.Unlock()

	paths, err := ResolvePNPMPaths(info)
	if err != nil {
		return PNPMPaths{}, err
	}
	logs.Info("checking PNPM toolchain: %s", info.ExactVersion)
	if fileExists(paths.PNPMExec) {
		logs.Info("PNPM toolchain ready: %s", paths.PNPMExec)
		frontendToolchainCacheMu.Lock()
		frontendPNPMCache[info.ExactVersion] = paths
		frontendToolchainCacheMu.Unlock()
		return paths, nil
	}
	if err := os.MkdirAll(paths.PNPMRoot, 0755); err != nil {
		return PNPMPaths{}, err
	}
	version := strings.TrimPrefix(info.ExactVersion, "pnpm")
	cmd := exec.Command(node.NPMExec, "install", "--global", "--prefix", paths.PNPMRoot, "--no-audit", "--no-fund", "--loglevel=error", "pnpm@"+version)
	cmd.Env = append(os.Environ(), "PATH="+node.NodeBin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_UPDATE_NOTIFIER=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	logs.Info("installing PNPM toolchain: pnpm@%s", version)
	if err := cmd.Run(); err != nil {
		if text := strings.TrimSpace(out.String()); text != "" {
			logs.Error("npm install output:\n%s", text)
		}
		return PNPMPaths{}, err
	}
	logs.Info("PNPM toolchain installed: %s", paths.PNPMExec)
	if !fileExists(paths.PNPMExec) {
		return PNPMPaths{}, fmt.Errorf("pnpm install failed: missing %s", paths.PNPMExec)
	}
	frontendToolchainCacheMu.Lock()
	frontendPNPMCache[info.ExactVersion] = paths
	frontendToolchainCacheMu.Unlock()
	return paths, nil
}

func PNPMCommand(node NodePaths, pnpm PNPMPaths, workDir string, args ...string) *exec.Cmd {
	cmd := exec.Command(pnpm.PNPMExec, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "PATH="+node.NodeBin+string(os.PathListSeparator)+pnpm.PNPMBin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_UPDATE_NOTIFIER=1", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0")
	return cmd
}

func nodeVersion(info NodeInfo) string { return "v" + strings.TrimPrefix(info.ExactVersion, "node") }
func nodeDistOS(platform Platform) string {
	if platform.OS == "darwin" {
		return "darwin"
	}
	return "linux"
}
func nodeDistArch(platform Platform) string {
	if platform.Arch == "amd64" {
		return "x64"
	}
	return "arm64"
}
func nodeArchiveRoot(info NodeInfo, platform Platform) string {
	return fmt.Sprintf("node-%s-%s-%s", nodeVersion(info), nodeDistOS(platform), nodeDistArch(platform))
}
func nodeArchiveName(info NodeInfo, platform Platform) string {
	return nodeArchiveRoot(info, platform) + ".tar.xz"
}
func nodeDownloadURL(info NodeInfo, platform Platform) string {
	v := nodeVersion(info)
	return fmt.Sprintf("https://nodejs.org/dist/%s/%s", v, nodeArchiveName(info, platform))
}
