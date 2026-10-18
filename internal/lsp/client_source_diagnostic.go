package lsp

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

const diagnosticClientFrameworkNotInstalled = "client-framework-not-installed"

func (s *Server) clientSourceDiagnostics(document Document, module *goxcompiler.Module) []Diagnostic {
	if module == nil || len(module.Clients) == 0 {
		return nil
	}

	root := s.projectRoot(document.Path)
	diagnostics := make([]Diagnostic, 0)
	installed := make(map[string]bool)
	entries := make(map[string]bool)
	for _, client := range module.Clients {
		framework := strings.ToLower(strings.TrimSpace(string(client.Framework)))
		if framework == "" {
			continue
		}

		frameworkRoot := filepath.Join(root, "client", framework)
		available, checked := installed[framework]
		if !checked {
			available = clientFrameworkInstalled(frameworkRoot)
			installed[framework] = available
		}
		if !available {
			if diagnostic, ok := clientFrameworkInstallationDiagnostic(document, client, framework); ok {
				diagnostics = append(diagnostics, diagnostic)
			}
			// Installation is the prerequisite. Do not report a missing entrypoint
			// for a framework workspace that does not exist yet.
			continue
		}

		target, ok := clientSourceTargetPath(frameworkRoot, framework, client.Entry)
		if !ok {
			// Invalid/escaping paths remain compiler diagnostics. This pass only
			// mirrors the filesystem existence checks requested by the LS contract.
			continue
		}
		missing, checked := entries[target]
		if !checked {
			info, err := os.Stat(target)
			missing = os.IsNotExist(err) || (err == nil && info.IsDir())
			entries[target] = missing
		}
		if !missing {
			continue
		}
		if diagnostic, ok := clientMissingEntryDiagnostic(document, client); ok {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return diagnostics
}

func clientFrameworkInstalled(frameworkRoot string) bool {
	packageInfo, err := os.Stat(filepath.Join(frameworkRoot, "package.json"))
	if err != nil || packageInfo.IsDir() {
		return false
	}
	nodeModulesInfo, err := os.Stat(filepath.Join(frameworkRoot, "node_modules"))
	return err == nil && nodeModulesInfo.IsDir()
}

func clientSourceTargetPath(frameworkRoot string, framework string, entry string) (string, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" || strings.Contains(entry, "\\") || filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") {
		return "", false
	}

	clean := path.Clean(entry)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", false
	}
	clean = strings.TrimPrefix(clean, "client/")
	clean = strings.TrimPrefix(clean, framework+"/")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", false
	}

	target := filepath.Join(frameworkRoot, filepath.FromSlash(clean))
	relative, err := filepath.Rel(frameworkRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}

func clientFrameworkInstallationDiagnostic(document Document, client goxcompiler.ClientDecl, framework string) (Diagnostic, bool) {
	startOffset := client.FrameworkOffset
	if startOffset < 0 || startOffset+len(framework) > len(document.Text) {
		return Diagnostic{}, false
	}
	start, err := document.Position(startOffset)
	if err != nil {
		return Diagnostic{}, false
	}
	end, err := document.Position(startOffset + len(framework))
	if err != nil {
		return Diagnostic{}, false
	}
	return Diagnostic{
		Range:    Range{Start: start, End: end},
		Severity: diagnosticSeverityError,
		Code:     diagnosticClientFrameworkNotInstalled,
		Source:   "gox",
		Message:  fmt.Sprintf("client framework %q is not installed; install it with: gox client add %s", framework, framework),
	}, true
}

func clientMissingEntryDiagnostic(document Document, client goxcompiler.ClientDecl) (Diagnostic, bool) {
	startOffset, endOffset, ok := clientEntryValueRange(document.Text, client.EntryOffset)
	if !ok {
		return Diagnostic{}, false
	}
	start, err := document.Position(startOffset)
	if err != nil {
		return Diagnostic{}, false
	}
	end, err := document.Position(endOffset)
	if err != nil {
		return Diagnostic{}, false
	}
	return Diagnostic{
		Range:    Range{Start: start, End: end},
		Severity: diagnosticSeverityError,
		Code:     "missing-client-entry",
		Source:   "gox",
		Message:  "client source file does not exist",
	}, true
}
