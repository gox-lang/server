package serverruntime

import (
	"time"

	"github.com/gox-lang/gox"
)

func (r *Runtime) bindPublicRuntimeInfo() {
	if r == nil {
		return
	}
	r.unbindServerInfo = gox.BindServerInfo(gox.ServerInfoBindings{
		ProjectName: func() string { return r.state().Config.ProjectName },
		InstanceID:  func() string { return r.instanceID },
		StartedAt:   func() time.Time { return r.startedAt },
		Host: func() string {
			host := r.state().Config.HTTP.Host
			if host == "" {
				return "localhost"
			}
			return host
		},
		Port: func() int {
			port := r.state().Config.HTTP.Port
			if port == 0 {
				return 3000
			}
			return port
		},
		IsDevelopment: func() bool { return r.state().Config.Dev.Enabled },
		IsClustered: func() bool {
			r.sessionStorageMu.RLock()
			enabled := r.clusterRuntime != nil
			r.sessionStorageMu.RUnlock()
			return enabled
		},
	})
	r.unbindClusterInfo = gox.BindClusterInfo(gox.ClusterInfoBindings{
		Enabled: func() bool { return gox.Server.IsClustered() },
		Adapter: func() string {
			r.sessionStorageMu.RLock()
			cluster := r.clusterRuntime
			if cluster == nil {
				r.sessionStorageMu.RUnlock()
				return ""
			}
			name := cluster.GetName()
			r.sessionStorageMu.RUnlock()
			return name
		},
	})
}
