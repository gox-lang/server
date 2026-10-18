package serverruntime

import (
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func TestRuntimeBindsPublicServerAndClusterInformation(t *testing.T) {
	runtime := &Runtime{instanceID: "instance-1", startedAt: time.Now().Add(-time.Second)}
	runtime.runtimeState.Store(&RuntimeState{Config: server.Config{
		ProjectName: "example",
		Dev:         server.DevConfig{Enabled: true},
		HTTP:        server.HTTPConfig{Host: "127.0.0.1", Port: 3001},
	}})
	runtime.clusterRuntime = &clusterTestRuntime{}
	runtime.bindPublicRuntimeInfo()
	defer runtime.unbindServerInfo()
	defer runtime.unbindClusterInfo()

	if gox.Server.ProjectName() != "example" || gox.Server.InstanceID() != "instance-1" {
		t.Fatalf("server identity = %q/%q", gox.Server.ProjectName(), gox.Server.InstanceID())
	}
	if gox.Server.Address() != "127.0.0.1:3001" || !gox.Server.IsDevelopment() || !gox.Server.IsClustered() {
		t.Fatalf("server information address=%q development=%v clustered=%v", gox.Server.Address(), gox.Server.IsDevelopment(), gox.Server.IsClustered())
	}
	if !gox.Cluster.Enabled() || gox.Cluster.Adapter() != "test-cluster" {
		t.Fatalf("cluster information enabled=%v adapter=%q", gox.Cluster.Enabled(), gox.Cluster.Adapter())
	}
}

func TestRuntimePublicInformationReadsCurrentReloadedState(t *testing.T) {
	runtime := &Runtime{instanceID: "instance-1", startedAt: time.Now()}
	runtime.runtimeState.Store(&RuntimeState{Config: server.Config{ProjectName: "before"}})
	runtime.bindPublicRuntimeInfo()
	defer runtime.unbindServerInfo()
	defer runtime.unbindClusterInfo()
	if gox.Server.ProjectName() != "before" || gox.Server.Address() != "localhost:3000" {
		t.Fatal("initial runtime information is incorrect")
	}
	runtime.runtimeState.Store(&RuntimeState{Config: server.Config{
		ProjectName: "after",
		HTTP:        server.HTTPConfig{Host: "0.0.0.0", Port: 4000},
	}})
	if gox.Server.ProjectName() != "after" || gox.Server.Address() != "0.0.0.0:4000" {
		t.Fatalf("reloaded runtime information project=%q address=%q", gox.Server.ProjectName(), gox.Server.Address())
	}
}
