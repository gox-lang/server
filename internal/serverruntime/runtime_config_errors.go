package serverruntime

import "github.com/gox-lang/gox"

func newRuntimeConfigError(kind string, message string, cause error) *GoxError {
	err := NewGoxError(gox.GoxErrorResolve, message, cause)
	err.Kind = kind
	err.Hint = runtimeConfigErrorHint(kind)
	translateGoxError(err)
	return err
}

func runtimeConfigErrorHint(kind string) string {
	switch kind {
	case "cluster-open-failed":
		return "Check the cluster adapter configuration and infrastructure availability."
	case "watcher-project-root-missing":
		return "Set the project root before starting the watcher."
	case "watcher-start-failed", "watcher-recursive-setup-failed", "hotreload-watcher-subscribe-failed", "cache404-watcher-subscribe-failed":
		return "Check file watcher permissions and restart the dev server."
	case "config-reload-before-init":
		return "Initialize the project before reloading runtime config."
	case "compile-resolver-ip-forwarded-failed", "compile-session-failed", "compile-ratelimit-failed", "compile-static-failed", "compile-preroute-failed":
		return "Check the corresponding server configuration section."
	case "unsupported-ratelimit-strategy":
		return "Use a supported rateLimit strategy."
	case "invalid-static-path":
		return "Use a valid static file path inside the project."
	case "static-cache-not-initialized":
		return "Initialize static file caching before resolving cached static content."
	case "unsupported-static-encoding":
		return "Use a supported static encoding."
	default:
		return "Check the server runtime configuration."
	}
}
