package serverruntime

import (
	"strings"

	"github.com/gox-lang/server/internal/clientbuilder"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
)

func (r *Runtime) startMissingFrameworkVendorsAsync(node toolchain.NodePaths, pnpm toolchain.PNPMPaths) {
	if r == nil {
		return
	}
	frameworks := clientbuilder.DetectFrameworkWorkspaces(r.projectRoot())
	missing := make([]string, 0, len(frameworks))
	for _, framework := range frameworks {
		if clientbuilder.FrameworkVendorOutputsExist(r.projectRoot(), framework) {
			logs.Info("client framework vendors ready: %s prod (existing)", framework)
			continue
		}
		missing = append(missing, framework)
	}
	if len(missing) == 0 {
		return
	}
	r.vendorBuildOnce.Do(func() {
		r.vendorBuildWG.Add(1)
		go func() {
			defer r.vendorBuildWG.Done()
			logs.Info("building missing client framework vendors asynchronously: %s", strings.Join(missing, ", "))
			if err := clientbuilder.BuildFrameworkVendors(node, pnpm, r.projectRoot(), missing); err != nil {
				logs.Error("client framework vendor build failed: %v", err)
				return
			}
			logs.Info("client framework vendor build completed: %s", strings.Join(missing, ", "))
		}()
	})
}

func (r *Runtime) waitForFrameworkVendors() {
	if r == nil {
		return
	}
	r.vendorBuildWG.Wait()
}
