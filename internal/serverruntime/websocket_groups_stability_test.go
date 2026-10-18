package serverruntime

import (
	"sync"
	"testing"

	"github.com/gox-lang/gox"
)

func TestDistributedGroupRuntimeSurvivesConcurrentMembershipChurn(t *testing.T) {
	cluster := &clusterTestRuntime{}
	runtime := &Runtime{instanceID: "local", clusterRuntime: cluster}
	runtime.bindGroups()
	defer runtime.unbindGroups()

	const socketCount = 64
	sockets := make([]*gox.Socket, socketCount)
	for index := range sockets {
		socket := registerLocalGroupSocket(t, runtime, "session")
		gox.BindSocketLocation(socket, runtime.instanceID, true)
		if err := runtime.registerSocketDirectory(socket); err != nil {
			t.Fatal(err)
		}
		sockets[index] = socket
	}

	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, socket := range sockets {
		socket := socket
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				if err := socket.Join("room"); err != nil {
					t.Error(err)
					return
				}
				_ = socket.Groups()
				_ = gox.Groups.Members("room").All()
				if err := socket.Leave("room"); err != nil {
					t.Error(err)
					return
				}
			}
			if err := socket.Join("room"); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	workers.Wait()
	if t.Failed() {
		return
	}
	if count := gox.Groups.CountMembers("room"); count != socketCount {
		t.Fatalf("distributed members after churn=%d, want %d", count, socketCount)
	}

	start = make(chan struct{})
	for _, socket := range sockets {
		socket := socket
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if err := socket.LeaveAll(); err != nil {
				t.Error(err)
			}
			if err := socket.LeaveAll(); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	workers.Wait()
	if gox.Groups.Count() != 0 || len(gox.Groups.List()) != 0 {
		t.Fatalf("distributed groups survived concurrent LeaveAll: %#v", gox.Groups.List())
	}
}
