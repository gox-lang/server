package serverruntime

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	clientmeta "github.com/gox-lang/server/internal/client"

	"github.com/gox-lang/gox"
)

func (r *Runtime) clientValuesRoute(ctx *gox.Context, req *http.Request) bool {
	if r == nil || ctx == nil || req == nil || ctx.Request.URL.Path() != clientmeta.ValuesPath {
		return false
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		ctx.Response.Headers.Set("Allow", "GET, HEAD")
		r.renderHTTPStatusError(ctx, req, nil, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}
	ctx.Response.Headers.Set("Content-Type", "text/javascript; charset=utf-8")
	ctx.Response.Headers.Set("Cache-Control", "no-store")
	if req.Method == http.MethodGet {
		ctx.Writer.WriteRawBytes(clientmeta.ValuesSource)
	}
	return true
}

func (r *Runtime) clientStaticRoute(ctx *gox.Context, req *http.Request) {
	if r == nil || ctx == nil || req == nil {
		return
	}

	path := ctx.Request.URL.Path()
	if !strings.HasPrefix(path, internalClientAssetPrefix) {
		return
	}

	state := r.stateFor(ctx)
	if state == nil || state.ClientAssets == nil {
		return
	}

	file, ok := state.ClientAssets.Resolve(path)
	if !ok || strings.TrimSpace(file) == "" {
		return
	}

	updateRequestRoute(ctx, gox.NewRoute(gox.RouteInput{
		Matched:       ctx.Request.Route.IsMatched(),
		Original:      ctx.Request.Route.Original(),
		Target:        file,
		Pattern:       path,
		Method:        resolveGoxRouteMethod(ctx),
		IsAction:      isGoxActionRequest(ctx),
		IsClientAsset: true,
		Params:        ctx.Request.Route.Params,
	}))
}

func isClientAssetRequestPath(path string) bool {
	return strings.HasPrefix(path, internalClientAssetPrefix)
}

func cleanBuildClientPath(projectRoot string, target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}

	full := target
	if !filepath.IsAbs(full) {
		full = filepath.Join(projectRoot, filepath.FromSlash(strings.TrimPrefix(target, "/")))
	}

	cleanRoot, err := filepath.Abs(filepath.Join(projectRoot, ".build", "client"))
	if err != nil {
		return "", false
	}
	cleanFull, err := filepath.Abs(full)
	if err != nil {
		return "", false
	}

	if cleanFull != cleanRoot && !strings.HasPrefix(cleanFull, cleanRoot+string(os.PathSeparator)) {
		return "", false
	}
	return cleanFull, true
}
