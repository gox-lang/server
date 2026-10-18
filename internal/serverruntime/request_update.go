package serverruntime

import "github.com/gox-lang/gox"

func updateRequestIP(ctx *gox.Context, ip string) {
	if ctx == nil {
		return
	}
	gox.SetRuntimeRequestIP(&ctx.Request, ip)
}

func updateRequestRoute(ctx *gox.Context, route gox.Route) {
	if ctx == nil {
		return
	}
	gox.SetRuntimeRequestRoute(&ctx.Request, route)
}
