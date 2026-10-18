package serverruntime

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
)

type runtimeHTTPShutdown interface{ Shutdown(context.Context) error }

const runtimeRestartShutdownTimeout = 10 * time.Second

func (r *Runtime) finishRuntimeRestart(httpServer runtimeHTTPShutdown, request RestartRequest) error {
	logs.Info("server restart requested: changed=%s affected=%d", request.ChangedRelPath, len(request.AffectedEntries))
	err := errors.Join(r.prepareRuntimeRestart(httpServer, request), r.Close())
	logs.Flush()
	if err != nil {
		return err
	}
	return &RestartRequestedError{Request: request}
}

func (r *Runtime) prepareRuntimeRestart(httpServer runtimeHTTPShutdown, request RestartRequest) error {
	if r == nil {
		return errors.New("runtime is not initialized")
	}

	var warmupErr error
	if r.dynamicWarmup != nil {
		warmupErr = r.dynamicWarmup.FlushRuntimeForRestart(r, request)
	}
	r.disconnectLocalWebSocketsForRestart()

	var shutdownErr error
	if httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeRestartShutdownTimeout)
		shutdownErr = httpServer.Shutdown(ctx)
		cancel()

	}
	return errors.Join(warmupErr, shutdownErr)
}

func (r *Runtime) disconnectLocalWebSocketsForRestart() int {
	if r == nil {
		return 0
	}
	sockets := r.webSocketRegistry().all()
	for _, socket := range sockets {
		if socket != nil {
			socket.Disconnect(gox.WSCloseRestart)
		}
	}
	return len(sockets)
}

func (r *Runtime) promoteRestartWarmupEntries(snapshot dynamicWarmupSnapshot, entries []hotreload.ReloadEntry) dynamicWarmupSnapshot {
	pageCount := nextRestartWarmupCount(snapshot.Pages)
	channelCount := nextRestartWarmupCount(snapshot.Channels)
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		route := r.findGoxRouteByEntry(entry)
		if route == nil {
			continue
		}
		path := strings.TrimSpace(route.RoutePath)
		if path == "" || path == goxErrorRoutePath {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if isChannelRoutePath(path) {
			snapshot.Channels = append(snapshot.Channels, dynamicWarmupCachePath{Path: path, Count: channelCount})
			continue
		}
		snapshot.Pages = append(snapshot.Pages, dynamicWarmupCachePath{Path: path, Count: pageCount})
	}
	return snapshot
}

func nextRestartWarmupCount(paths []dynamicWarmupCachePath) int64 {
	var maximum int64
	for _, item := range paths {
		if item.Count > maximum {
			maximum = item.Count
		}
	}
	return maximum + 1
}
