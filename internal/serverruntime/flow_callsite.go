package serverruntime

import (
	goruntime "runtime"
	"strings"

	"github.com/gox-lang/gox"
)

func init() {
	gox.SetRuntimeFlowCallsiteResolver(responseFlowCallsite)
	gox.SetRuntimeResponseMutationGuard(responseMutationAllowed)
}

func responseMutationAllowed(response *gox.Response) bool {
	if response == nil {
		return true
	}
	value := gox.RuntimeResponseState(response)
	if value == nil {
		return true
	}
	state, ok := value.(*responseRuntimeState)
	if !ok || state == nil {
		return true
	}
	return !state.wroteHeader && !state.bodyWritten && !state.bodyConstructed
}

func responseFlowCallsite() (string, int) {
	for skip := 1; skip <= 16; skip++ {
		_, file, line, ok := goruntime.Caller(skip)
		if !ok || file == "" {
			continue
		}
		slash := strings.ReplaceAll(file, "\\", "/")
		if strings.HasSuffix(slash, ".gox") || strings.Contains(slash, "/pages/") {
			return normalizeUserFacingPath(slash), line
		}
	}
	return "", 0
}

func normalizeUserFacingPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	slash := strings.ReplaceAll(path, "\\", "/")
	slash = strings.TrimPrefix(slash, "./")
	if idx := strings.Index(slash, "/pages/"); idx >= 0 {
		return slash[idx+1:]
	}
	return slash
}
